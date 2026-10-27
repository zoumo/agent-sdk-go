#!/usr/bin/env python3
"""Legacy offline replay for historical Go-driven story datasets.

New live capture uses tools/recording and the official Python SDKs.
"""
import argparse
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

STORIES = [f'SDK-US-{i:03}' for i in range(1, 20)]
PROMPTS = {
    '001': ['Reply with the word Ready.', 'Reply with the word Done.'],
    '002': ['Reply with the word Structured.'],
    '003': ['Produce a numbered list from one to twenty.', 'Also include the word sample.'],
    '004': ['Use only the two synthetic fixture tools to read numbers.txt, add the integers, and write summary.txt. Then report the total briefly.'],
    '005': ['Reply with the word Ready.'],
    '008': [],
    '009': ['Use Write exactly once to create summary.txt containing exactly total=18 followed by a newline. Do not use other tools.', 'Use Write exactly once to create denied.txt containing exactly denied followed by a newline. If denied, finish without retrying.'],
    '010': ['Use Read exactly once to read numbers.txt. Then report the two integers briefly. Do not use other tools.'],
    '011': ["Use AskUserQuestion exactly once. Ask 'Which synthetic color?' with exactly two options Blue and Green. Wait for the answer, then finish briefly. Use no other tools."],
    '012': ['Reply with the word Ready.'],
    '013': ['Reply with the word Ready.', 'Reply with the word Done.'],
    '014': ['Produce a numbered list from one to twenty.'],
    '015': ['Reply with the word Ready.'],
    '016': ['Produce a numbered list from one to twenty.'],
    '017': ['Reply with the word Ready.'],
    '018': [],
    '019': ['Reply with the word Saved.'],
}

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

def prepare(args):
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
    driver = Path(args.driver).resolve(strict=True)
    source = Path(args.source_config or (Path.home() / ('.codex' if args.provider == 'codex' else '.claude'))).resolve(strict=True)
    version = subprocess.run([str(cli), '--version'], cwd=target, capture_output=True, text=True, check=True, timeout=10).stdout.strip()
    match = re.search(r'\d+\.\d+\.\d+', version)
    if not match:
        raise ValueError('native version unavailable')
    version = match.group()
    policy = json.loads(subprocess.check_output([str(driver), '--story-template', 'SDK-US-001'], cwd=target, timeout=10))
    if version not in policy['cliVersions']:
        raise ValueError('unreviewed native recording version')
    conf = {}
    name = 'config.toml' if args.provider == 'codex' else 'settings.json'
    if (source / name).exists():
        raw = read_regular(source / name)
        conf = tomllib.loads(raw.decode()) if args.provider == 'codex' else json.loads(raw)
    repo = Path(__file__).resolve().parent.parent
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip()
    # Hash actual tracked/untracked source bytes, never machine configuration.
    files = subprocess.check_output(['git', 'ls-files', '-co', '--exclude-standard', '-z'], cwd=repo).split(b'\0')
    h = hashlib.sha256()
    for filename in sorted(set(files)):
        if filename:
            path = repo / os.fsdecode(filename)
            if path.is_file():
                h.update(filename + b'\0' + hashlib.sha256(path.read_bytes()).digest())
    for story in args.stories.split(',') if args.stories else STORIES:
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

        native_args = json.loads(subprocess.check_output([str(driver), '--story-template', story], cwd=root, timeout=10))['args']
        prompts = PROMPTS.get(story[-3:], [])
        if args.provider == 'codex' and story.endswith('004'):
            prompts = ['Use fixture_read exactly once on numbers.txt, add the two integers, then use fixture_write exactly once on summary.txt with text exactly sum=42 followed by a newline. Use no other tools.']
        if args.provider == 'codex' and story[-3:] in ('010', '011'):
            prompts = ['Synthetic interaction fixture. Use request_user_input exactly once to ask which color to use: Amber or Indigo. Give exactly those two choices. After receiving the answer, report the chosen color briefly and finish. Do not use other tools or spawn agents.']
        if args.provider == 'claude' and story.endswith('004'):
            prompts = ['Use Read once on numbers.txt (7 and 11), then Write once on summary.txt with exactly total=18 followed by a newline. Use no other tools.']
        model = args.model or (conf.get('model', '') if args.provider == 'codex' else env.get('ANTHROPIC_MODEL', conf.get('model', '')))
        plan = {'version': 1, 'provider': args.provider, 'story': story, 'cli': str(cli),
                'cliVersion': version, 'cliSHA256': hashlib.sha256(cli.read_bytes()).hexdigest(),
                'driverSHA256': hashlib.sha256(driver.read_bytes()).hexdigest(), 'sdkRevision': revision,
                'sourceSHA256': h.hexdigest(), 'root': str(root), 'home': str(home), 'config': str(config),
                'project': str(project), 'raw': str(root / 'raw'), 'model': model,
                'args': native_args, 'prompts': prompts}
        (project / 'numbers.txt').write_text('17\n25\n' if args.provider == 'codex' else '7\n11\n')
        os.chmod(project / 'numbers.txt', 0o600)
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
    captured_text = data + ''.join(path.read_text(errors='replace') for path in (root / 'raw').iterdir() if path.is_file() and path.name != 'raw.jsonl')
    scan = not any(secret in captured_text for secret in secrets)
    saved = {}
    for path in (root / 'config/projects').rglob('*.jsonl') if (root / 'config/projects').exists() else []:
        if path.is_symlink() or not path.is_file():
            raise ValueError('saved source is not a regular file')
        saved[str(path.relative_to(root))] = hashlib.sha256(read_regular(path, 16 << 20)).hexdigest()
    result = {'provenance': 'real-private' if native_frames else ('launch-attempt' if raw.exists() else 'not-executed'),
              'provider': plan['provider'], 'story': plan['story'], 'status': report['status'],
              'cliVersion': plan['cliVersion'], 'cliSHA256': plan['cliSHA256'],
              'driverSHA256': plan['driverSHA256'], 'sdkRevision': plan['sdkRevision'],
              'sourceSHA256': plan['sourceSHA256'], 'files': captures, 'savedFiles': saved,
              'promptSHA256': [hashlib.sha256(text.encode()).hexdigest() for text in plan['prompts']],
              'inputs': report.get('inputs', 0), 'results': report.get('results', 0), 'frames': len(frames), 'nativeFrames': native_frames,
              'independentHome': True, 'published': False, 'credentialScan': 'pass' if scan else 'quarantined',
              'gaps': report.get('gaps', [])}
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
    for name, digest in item['files'].items():
        path = root / 'raw' / name
        if Path(name).name != name or path.is_symlink() or hashlib.sha256(read_regular(path, 16 << 20)).hexdigest() != digest:
            raise ValueError('recording changed')
    for name, digest in item.get('savedFiles', {}).items():
        path = root / name
        if not name.startswith('config/projects/') or '..' in Path(name).parts or path.is_symlink() or hashlib.sha256(read_regular(path, 16 << 20)).hexdigest() != digest:
            raise ValueError('saved recording source changed')

def execute(args):
    target = Path(args.output).resolve(strict=True)
    suite = json.loads((target / 'suite.json').read_text())
    driver = args.driver if args.action == 'replay' and args.driver else suite['driver']
    results = []
    for root in sorted(target.glob('SDK-US-*')):
        if args.stories and root.name not in args.stories.split(','):
            continue
        plan = root / 'plan.json'
        mode = '--story-replay' if args.action == 'replay' else '--story-plan'
        if args.action == 'replay' and not (root / 'raw/raw.jsonl').exists():
            continue
        if args.action == 'replay':
            verify_recording(root)
            (root / 'replay-peer.json').unlink(missing_ok=True)
        env = {k: v for k, v in os.environ.items() if not k.startswith(('ANTHROPIC_', 'OPENAI_', 'CODEX_API_'))}
        if args.action == 'record':
            env['SDK_REAL_AGENT_E2E'] = '1'
        try:
            proc = subprocess.Popen([driver, mode, str(plan)], cwd=root / 'project', env=env,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
            proc.communicate(timeout=135)
        except subprocess.TimeoutExpired:
            import signal
            os.killpg(proc.pid, signal.SIGTERM)
            try:
                proc.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid, signal.SIGKILL)
                proc.communicate(timeout=5)
            scrub_auth(root)
            print(root.name + ': coordinator timeout; stopped')
            return 1
        filename = 'replay-report.json' if args.action == 'replay' else 'report.json'
        report = json.loads((root / filename).read_text()) if (root / filename).exists() else {'status': 'failed'}
        if args.action == 'record' and not (root / 'manifest.json').exists():
            try:
                scanned = manifest(root, report)
            finally:
                scrub_auth(root)
            if not scanned:
                print(root.name + ': credential scan quarantined; stopped')
                return 1
        status = report['status']
        code = proc.returncode
        if args.action == 'replay' and code:
            original = json.loads((root / 'report.json').read_text())
            before, after = root / 'diagnostics.private.json', root / 'replay-diagnostics.private.json'
            full = root / 'replay-peer.json'
            if (original['status'] == 'failed' and report['status'] == 'failed' and before.exists() and after.exists()
                    and before.read_bytes() == after.read_bytes() and full.exists()
                    and json.loads(full.read_text()).get('fullConsumption')
                    and original['inputs'] == report['inputs'] and original['results'] == report['results']):
                status, code = 'expected-failure-reproduced', 0
        print(root.name + ': ' + status)
        results.append({'story': root.name, 'status': status, 'exitCode': code})
        if code:
            print('Stopped after failure; private diagnostics retained. No retry.')
            break
    name = 'replay-suite-result.json' if args.action == 'replay' else 'suite-result.json'
    # Never replace the first result of an attempted recording.
    result_path = target / name
    if result_path.exists():
        name = str(len(list(target.glob(args.action + '-result-*.json'))) + 1)
        result_path = target / (args.action + '-result-' + name + '.json')
    private_json(result_path, results)
    return int(any(item['exitCode'] for item in results))

def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['prepare', 'record', 'replay'])
    parser.add_argument('--provider', choices=['codex', 'claude'])
    parser.add_argument('--output', required=True)
    parser.add_argument('--driver')
    parser.add_argument('--cli')
    parser.add_argument('--source-config')
    parser.add_argument('--stories', help='comma-separated SDK-US IDs; defaults to all 19')
    parser.add_argument('--model')
    args = parser.parse_args()
    if args.action != 'replay':
        print('Live stories moved to tools/recording/agent_recording/bootstrap.py (official Python SDKs).', file=sys.stderr)
        return 2
    try:
        if args.action == 'prepare':
            if not args.provider or not args.driver:
                parser.error('prepare requires --provider and --driver')
            prepare(args)
            return 0
        return execute(args)
    except Exception:
        print('Recording operation failed; private values and diagnostics withheld.', file=sys.stderr)
        return 1

if __name__ == '__main__':
    sys.exit(main())
