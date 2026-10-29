#!/usr/bin/env python3
"""Install the story wheel independently and exercise its external Go boundary."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import venv


def check(wheel):
    binary = Path(os.environ['AGENT_MOCK_BIN']).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix='record-user-story-installed-') as temporary:
        root = Path(temporary).resolve()
        venv.EnvBuilder(with_pip=True, symlinks=True).create(root/'env')
        bin_dir = root/'env/bin'
        python = bin_dir/'python'
        env = {'PATH': str(bin_dir) + os.pathsep + os.defpath, 'HOME': str(root),
               'PIP_CONFIG_FILE': os.devnull, 'AGENT_MOCK_BIN': str(binary)}

        def run(command, data=None):
            result = subprocess.run([str(part) for part in command], cwd=root, env=env,
                                    input=data, capture_output=True, timeout=60)
            if result.returncode:
                raise RuntimeError(f'{command[0]} failed: {result.stderr.decode(errors="replace")}')
            return result.stdout

        run([python, '-I', '-m', 'pip', 'install', '--no-index', '--no-deps', wheel])
        run([python, '-I', '-c', '''
import importlib.util
from importlib.metadata import distribution
from pathlib import Path
import record_user_story
assert Path(record_user_story.__file__).is_relative_to(Path.cwd() / 'env')
for name in ('agent_mock', 'openai_codex', 'claude_agent_sdk', 'record_user_story.mock'):
    assert importlib.util.find_spec(name) is None, name
assert all('extra ==' in item for item in distribution('record-user-story').requires or [])
assert (Path(record_user_story.__file__).parent / 'schemas/wire.schema.json').is_file()
assert {e.name for e in distribution('record-user-story').entry_points} == {'record-user-story'}
'''])
        catalog = json.loads(run([bin_dir/'record-user-story', 'catalog']))
        assert {(item['provider'], item['storyId'], item['caseId']) for item in catalog} == {
            ('codex', 'conversation', 'two-inputs'), ('claude', 'conversation', 'two-inputs')}
        peer = json.loads(run([python, '-I', '-c',
                              'import json; from record_user_story.peer import identity; print(json.dumps(identity()))']))
        for provider in ('codex', 'claude'):
            definition = json.loads(run([python, '-I', '-c',
                'import json, sys; from record_user_story.scenarios import recipe; '
                'print(json.dumps(recipe(sys.argv[1], "conversation", case_id="two-inputs")))', provider]))
            case = root/provider/'conversation'/'two-inputs'
            case.mkdir(parents=True)
            for directory in ('home', 'config', 'project', 'tmp'):
                (case/directory).mkdir()
            for name, contents in definition['files'].items():
                target = case/'project'/name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(contents)
            native = case/'native.py'
            native.write_text('import sys\nfor line in sys.stdin.buffer:\n sys.stdout.buffer.write(line); sys.stdout.buffer.flush()\n')
            plan = dict(version=4, provider=provider, story='conversation', storyId='conversation',
                        caseId='two-inputs', driverProfile=definition['driverProfile'], scenario=definition,
                        scenarioSHA256=hashlib.sha256(json.dumps(definition, sort_keys=True, separators=(',', ':')).encode()).hexdigest(),
                        prompts=definition['prompts'], cli=str(python.resolve()),
                        cliSHA256=hashlib.sha256(python.read_bytes()).hexdigest(), cliVersion='synthetic',
                        root=str(case), home=str(case/'home'), config=str(case/'config'),
                        project=str(case/'project'), raw=str(case/'raw'), tempRoot=str(root.parent),
                        args=[str(native)], model='', agentMock=peer)
            (case/'plan.json').write_text(json.dumps(plan))
            key = 'CODEX_HOME' if provider == 'codex' else 'CLAUDE_CONFIG_DIR'
            (case/'native.env.private.json').write_text(json.dumps({'HOME':plan['home'], key:plan['config'], 'TMPDIR':str(case/'tmp')}))
            wire = b'{"native":900719925474099312345,"decimal":1.2300}\n'
            env['AGENT_MOCK_RECORD'] = '1'
            assert run([bin_dir/'record-user-story', 'proxy', '--plan', case/'plan.json'], wire) == wire
            env.pop('AGENT_MOCK_RECORD')
            transport = json.loads((case/'raw/transport.json').read_bytes())
            assert transport['captureStatus'] == 'complete' and transport['cleanupComplete']
            native.unlink()
            assert run([bin_dir/'record-user-story', 'mock-proxy', '--plan', case/'plan.json',
                        '--scenario', case/'raw/raw.jsonl'], wire) == wire
            proof = json.loads((case/'replay-peer.json').read_bytes())
            assert proof['fullConsumption'] and proof['clientEOF'] and not proof['nativeLaunched']
            assert proof['peer'] == 'Go native mock'
            assert json.loads((case/'replay-peer-identity.json').read_bytes()) == peer
        run([python, '-I', '-m', 'record_user_story', '--help'])
        for old in ('agent-mock', 'mock-codex', 'mock-claude'):
            assert not (bin_dir/old).exists()
    print('Installed record-user-story wheel: catalog, assets, Go record/mock for both protocols, numeric bytes and identities passed outside checkout; no SDK/model execution.')


if __name__ == '__main__':
    if len(sys.argv) != 2:
        raise SystemExit('usage: check-agent-mock-package.py RECORD_USER_STORY_WHEEL (requires AGENT_MOCK_BIN)')
    check(Path(sys.argv[1]).resolve(strict=True))
