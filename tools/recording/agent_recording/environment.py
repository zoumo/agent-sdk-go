#!/usr/bin/env python3
"""Prepare, record, or replay one private dataset per SDK user story.

No model runs without the explicit record subcommand. Never prints credentials,
raw diagnostics, endpoint values or inherited configuration. Python >= 3.11.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tomllib

from .scenarios import STORIES, recipe, digest, scenario_directory, scenario_assets

def private_json(path, value):
    with path.open('x', encoding='utf-8') as f:
        os.chmod(path, 0o600)
        json.dump(value, f, indent=2)
        f.write('\n')
        f.flush()
        os.fsync(f.fileno())

def read_regular(path, limit=1 << 20):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_size > limit:
            raise ValueError('configuration source must be a bounded regular file')
        return os.read(fd, limit + 1)
    finally:
        os.close(fd)

def config_environment(settings, inherited):
    """Select endpoint+credentials as a unit, preventing platform overrides.

    If the selected configuration supplies an endpoint, authentication must also
    come from that configuration (or its isolated opaque credential file).
    Otherwise retain the explicitly selected process endpoint/auth pair.
    """
    keys = ['ANTHROPIC_BASE_URL', 'ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_API_KEY',
            'ANTHROPIC_MODEL', 'ANTHROPIC_DEFAULT_SONNET_MODEL',
            'ANTHROPIC_DEFAULT_OPUS_MODEL', 'ANTHROPIC_DEFAULT_HAIKU_MODEL']
    selected = settings.get('env', {})
    source = selected if selected.get('ANTHROPIC_BASE_URL') else inherited
    result = {k: source[k] for k in keys if isinstance(source.get(k), str)}
    for k in keys[3:]:
        if isinstance(selected.get(k), str):
            result[k] = selected[k]
    return result

def toml_value(value):
    if isinstance(value, str):
        return json.dumps(value)
    if isinstance(value, bool):
        return str(value).lower()
    if isinstance(value, int):
        return str(value)
    if isinstance(value, dict):
        return '{' + ','.join(json.dumps(k) + '=' + toml_value(v) for k, v in value.items()) + '}'
    raise ValueError('unsupported inherited connection value')

def codex_config(source):
    """Keep connection selection only, never projects, instructions, MCP or hooks."""
    out = {}
    for key in ('model', 'model_provider', 'model_reasoning_effort'):
        if key in source:
            out[key] = source[key]
    provider = source.get('model_provider')
    if provider and provider in source.get('model_providers', {}):
        allowed = {'name', 'base_url', 'env_key', 'wire_api', 'requires_openai_auth',
                   'http_headers', 'env_http_headers', 'supports_websockets'}
        item = {k: v for k, v in source['model_providers'][provider].items() if k in allowed}
        item.update(request_max_retries=0, stream_max_retries=0)
        out['model_providers'] = {provider: item}
    return out

def story_prompts(provider, story, child_case=False):
    if child_case:
        from .child_policy import root_prompt
        if story[-3:] not in ('006', '007'):
            raise ValueError('child case only applies to SDK-US-006/007')
        return [root_prompt(provider, story)]
    return recipe(provider, story)['prompts']


def prepare(args):
    selected = getattr(args, 'stories', None).split(',') if getattr(args, 'stories', None) else STORIES
    definitions = [recipe(args.provider, story) for story in selected]
    if not getattr(args, 'child_case', False) and not any(item['mode'] == 'live-selected-path' for item in definitions):
        raise ValueError('selection has no live recording path; use catalog or offline')
    identity = sdk_identity(args.provider)
    if getattr(args, 'child_case', False) and any(story[-3:] not in ('006', '007') for story in selected):
        raise ValueError('child case only applies to SDK-US-006/007')
    target = Path(args.output)
    if not target.is_absolute() or target.exists():
        raise ValueError('output must be a new absolute directory outside Git')
    parent = target.parent.resolve(strict=True)
    target = parent / target.name
    for ancestor in (parent, *parent.parents):
        if (ancestor / '.git').is_file() or (ancestor / '.git/HEAD').is_file():
            raise ValueError('private output must be outside Git')
    target.mkdir(mode=0o700)
    cli = Path(args.cli or shutil.which(args.provider) or '').resolve(strict=True)
    driver = Path(__file__).resolve().with_name('bootstrap.py')
    source = Path(args.source_config or (Path.home() / ('.codex' if args.provider == 'codex' else '.claude'))).resolve(strict=True)
    version = subprocess.run([str(cli), '--version'], cwd=target, capture_output=True, text=True, check=True, timeout=10).stdout.strip()
    match = re.search(r'\d+\.\d+\.\d+', version)
    if not match:
        raise ValueError('native version unavailable')
    version = match.group()
    from .policy import reviewed_versions, native_arguments
    policy = {'cliVersions': reviewed_versions(args.provider)}
    if version not in policy['cliVersions']:
        raise ValueError('unreviewed native recording version')
    conf = {}
    name = 'config.toml' if args.provider == 'codex' else 'settings.json'
    if (source / name).exists():
        raw = read_regular(source / name)
        conf = tomllib.loads(raw.decode()) if args.provider == 'codex' else json.loads(raw)
    repo = Path(__file__).resolve().parents[3]
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip()
    # Hash actual tracked/untracked source bytes, never machine configuration.
    files = subprocess.check_output(['git', 'ls-files', '-co', '--exclude-standard', '-z'], cwd=repo).split(b'\0')
    h = hashlib.sha256()
    for filename in sorted(set(files)):
        if filename:
            path = repo / os.fsdecode(filename)
            if path.is_file():
                h.update(filename + b'\0' + hashlib.sha256(path.read_bytes()).digest())
    for story in selected:
        if not getattr(args, 'child_case', False):
            definition = recipe(args.provider, story)
            if definition['mode'] != 'live-selected-path':
                print(story + ': ' + definition['mode'] + '; no configuration copied; use offline')
                continue
        if story not in STORIES:
            raise ValueError('unknown user story')
        root = target / story
        root.mkdir(mode=0o700)
        for child in ['home', 'config', 'project', 'raw', 'tmp']:
            (root / child).mkdir(mode=0o700)
        home, config, project = root / 'home', root / 'config', root / 'project'
        env = {'HOME': str(home), 'TMPDIR': str(root / 'tmp'), 'PATH': '/usr/local/bin:/usr/bin:/bin',
               'LANG': 'C.UTF-8', 'CLAUDE_CODE_DISABLE_AUTO_MEMORY': '1'}
        # Only transport proxy settings are inherited, never the whole environment.
        for key in ['HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'NO_PROXY', 'SSL_CERT_FILE', 'SSL_CERT_DIR']:
            if key in os.environ:
                env[key] = os.environ[key]
        if args.provider == 'codex':
            env['CODEX_HOME'] = str(config)
            filtered = codex_config(conf)
            lines = [json.dumps(k) + '=' + toml_value(v) for k, v in filtered.items()]
            with (config / 'config.toml').open('x') as f:
                os.chmod(f.name, 0o600)
                f.write('\n'.join(lines) + '\n')
            keys = {'OPENAI_API_KEY', 'OPENAI_BASE_URL', 'CODEX_API_KEY'}
            for item in filtered.get('model_providers', {}).values():
                if item.get('env_key'):
                    keys.add(item['env_key'])
                keys.update(item.get('env_http_headers', {}).values())
            for key in keys:
                if key in os.environ:
                    env[key] = os.environ[key]
            if (source / 'auth.json').exists():
                with (config / 'auth.json').open('xb') as f:
                    os.chmod(f.name, 0o600)
                    f.write(read_regular(source / 'auth.json'))

        else:
            env.update(config_environment(conf, os.environ))
            env.update(CLAUDE_CONFIG_DIR=str(config), CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS='1', DISABLE_AUTOUPDATER='1')
            for filename in ['.credentials.json']:
                if (source / filename).exists():
                    with (config / filename).open('xb') as f:
                        os.chmod(f.name, 0o600)
                        f.write(read_regular(source / filename))

        snapshot = root / 'driver' / 'agent_recording'
        snapshot.mkdir(parents=True, mode=0o700)
        for module in Path(__file__).parent.glob('*.py'):
            with (snapshot / module.name).open('xb') as out:
                os.chmod(out.name, 0o600)
                out.write(module.read_bytes())
        for asset in scenario_assets():
            destination = snapshot.parent / 'scenarios' / asset.relative_to(scenario_directory())
            destination.parent.mkdir(parents=True, mode=0o700, exist_ok=True)
            with destination.open('xb') as out:
                os.chmod(out.name, 0o600)
                out.write(read_regular(asset))
        child_case = bool(getattr(args, 'child_case', False))
        native_args = native_arguments(args.provider, story, child_case)
        prompts = story_prompts(args.provider, story, child_case)
        if child_case:
            from .child_policy import CHILD_FILE, CHILD_TEXT, role_file
            (project / CHILD_FILE).write_text(CHILD_TEXT)
            os.chmod(project / CHILD_FILE, 0o600)
            if args.provider == 'codex':
                (config / 'agents').mkdir(mode=0o700)
                (config / 'agents/fixture_reader.toml').write_text(role_file())
                os.chmod(config / 'agents/fixture_reader.toml', 0o600)
        model = args.model or (conf.get('model', '') if args.provider == 'codex' else env.get('ANTHROPIC_MODEL', conf.get('model', '')))
        definition = recipe(args.provider, story)
        plan = {'version': 3, 'provider': args.provider, 'story': story, 'cli': str(cli),
                'cliVersion': version, 'cliSHA256': hashlib.sha256(cli.read_bytes()).hexdigest(),
                'driverSHA256': hashlib.sha256(driver.read_bytes()).hexdigest(), 'sdkRevision': revision,
                'sourceSHA256': h.hexdigest(), 'root': str(root), 'home': str(home), 'config': str(config),
                'project': str(project), 'raw': str(root / 'raw'), 'model': model,
                'args': native_args, 'prompts': prompts,
                'scenario': definition, 'scenarioSHA256': digest(definition),
                'officialSDK': identity, 'python': str(Path(sys.executable).absolute()),
                'driver': str(snapshot / 'bootstrap.py'), 'childCase': child_case}
        if not child_case:
            for name, content in definition['files'].items():
                path = project / name
                path.parent.mkdir(parents=True, mode=0o700, exist_ok=True)
                path.write_text(content)
                os.chmod(path, 0o600)
        # Preserve the independently evolving, explicitly selected child profile
        # on its native v2 driver contract; v3 scenarios do not certify it.
        if child_case:
            plan['version'] = 2
            plan.pop('scenario')
            plan.pop('scenarioSHA256')
            from .child_policy import case_contract
            plan['case'] = case_contract(story)
        private_json(root / 'native.env.private.json', env)
        private_json(root / 'plan.json', plan)
    private_json(target / 'suite.json', {'provider': args.provider, 'driver': str(driver), 'cliVersion': version,
                                        'sourceSHA256': h.hexdigest(), 'sdkRevision': revision})
    print('Prepared private per-story plans with independent HOME; no model calls.')

def manifest(root, report):
    plan = json.loads((root / 'plan.json').read_text())
    captures = {}
    for path in (root / 'raw').iterdir():
        if path.is_file():
            captures[path.name] = hashlib.sha256(path.read_bytes()).hexdigest()
    raw = root / 'raw/raw.jsonl'
    data = raw.read_text() if raw.exists() else ''
    frames = [json.loads(line) for line in data.splitlines()]
    native_frames = sum(item['direction'] == 's2c' for item in frames)
    env = json.loads((root / 'native.env.private.json').read_text())
    secrets = [value for key, value in env.items() if value and ('TOKEN' in key or 'KEY' in key)]
    def tokens(value):
        if isinstance(value, dict):
            for key, item in value.items():
                if isinstance(item, str) and item and any(word in key.lower() for word in ('token', 'api_key', 'apikey')):
                    secrets.append(item)
                elif isinstance(item, (dict, list)):
                    tokens(item)
        elif isinstance(value, list):
            for item in value:
                tokens(item)
    for filename in ['auth.json', '.credentials.json']:
        path = root / 'config' / filename
        if path.exists():
            tokens(json.loads(read_regular(path)))
    for filename in ['config.toml']:
        path = root / 'config' / filename
        if path.exists():
            for item in tomllib.loads(read_regular(path).decode()).get('model_providers', {}).values():
                secrets.extend(value for value in item.get('http_headers', {}).values() if value)
                names = [item.get('env_key'), *item.get('env_http_headers', {}).values()]
                secrets.extend(env[name] for name in names if name and env.get(name))
    captured_text = ''.join(path.read_text(errors='replace') for path in root.glob('*report.json')) + ''.join(path.read_text(errors='replace') for path in root.glob('*diagnostics.private.json')) + data + ''.join(path.read_text(errors='replace') for path in (root / 'raw').iterdir() if path.is_file() and path.name != 'raw.jsonl')
    scan = not any(secret in captured_text for secret in secrets)
    saved = {}
    for path in (root / 'config/projects').rglob('*.jsonl') if (root / 'config/projects').exists() else []:
        if path.is_symlink() or not path.is_file():
            raise ValueError('saved source is not a regular file')
        saved[str(path.relative_to(root))] = hashlib.sha256(read_regular(path, 16 << 20)).hexdigest()
    result = {'provenance': 'real-private' if native_frames else ('launch-attempt' if raw.exists() else 'not-executed'),
              'provider': plan['provider'], 'story': plan['story'], 'status': report['status'],
              'cliVersion': plan['cliVersion'], 'cliSHA256': plan['cliSHA256'],
              'driverSHA256': plan['driverSHA256'], 'sdkRevision': plan['sdkRevision'], 'officialSDK': plan['officialSDK'],
              'driverFiles': {str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest() for p in (root / 'driver').rglob('*') if p.is_file() and p.suffix in ('.py', '.json')},
              'sourceSHA256': plan['sourceSHA256'], 'files': captures, 'savedFiles': saved,
              'planSHA256': hashlib.sha256(read_regular(root / 'plan.json')).hexdigest(),
              'scenarioSHA256': plan.get('scenarioSHA256'),
              'promptSHA256': [hashlib.sha256(text.encode()).hexdigest() for text in plan['prompts']],
              'caseFiles': {name: hashlib.sha256((root / name).read_bytes()).hexdigest() for name in ['project/child-fixture.txt', 'config/agents/fixture_reader.toml'] if (root / name).exists()},
              'inputs': report.get('inputs', 0), 'results': report.get('results', 0), 'frames': len(frames), 'nativeFrames': native_frames,
              'independentHome': True, 'published': False, 'credentialScan': 'pass' if scan else 'quarantined',
              'gaps': report.get('gaps', [])}
    if 'case' in report:
        result['case'] = report['case']
    private_json(root / 'manifest.json', result)
    return scan

def scrub_auth(root):
    # Only exact generated paths, never any path from the source configuration.
    for path in [root / 'native.env.private.json', root / 'config/auth.json',
                 root / 'config/.credentials.json', root / 'config/config.toml']:
        if path.is_symlink():
            raise ValueError('generated authentication file replaced')
        path.unlink(missing_ok=True)

def verify_recording(root):
    item = json.loads((root / 'manifest.json').read_text())
    if item.get('planSHA256') and hashlib.sha256(read_regular(root / 'plan.json')).hexdigest() != item['planSHA256']:
        raise ValueError('recording plan changed')
    for name, digest in item['files'].items():
        path = root / 'raw' / name
        if Path(name).name != name or path.is_symlink() or hashlib.sha256(read_regular(path, 16 << 20)).hexdigest() != digest:
            raise ValueError('recording changed')
    for name, digest in item.get('caseFiles', {}).items():
        if name not in ['project/child-fixture.txt', 'config/agents/fixture_reader.toml'] or hashlib.sha256(read_regular(root / name)).hexdigest() != digest:
            raise ValueError('synthetic child case file changed')
    for name, digest in item.get('driverFiles', {}).items():
        path = root / name
        if not name.startswith('driver/') or '..' in Path(name).parts or path.is_symlink() or hashlib.sha256(read_regular(path)).hexdigest() != digest:
            raise ValueError('recording driver snapshot changed')
    for name, digest in item.get('savedFiles', {}).items():
        path = root / name
        if not name.startswith('config/projects/') or '..' in Path(name).parts or path.is_symlink() or hashlib.sha256(read_regular(path, 16 << 20)).hexdigest() != digest:
            raise ValueError('saved recording source changed')


def sdk_identity(provider):
    from importlib.metadata import version
    from .policy import SDK_VERSIONS
    name, expected = SDK_VERSIONS[provider]
    actual = version(name)
    if actual != expected:
        raise ValueError('official SDK version does not match lock')
    return {'package': name, 'version': actual}
