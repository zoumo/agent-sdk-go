#!/usr/bin/env python3
"""Reviewed recording admission and a fail-closed, current-source simulation gate."""
import argparse
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import sys
import uuid

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_INDEX = ROOT / 'tests/simulation/recordings/index.json'
SCENARIOS = ROOT / 'tests/simulation/scenarios'
SOURCE_EVIDENCE = ('plan.json', 'manifest.json', 'report.json', 'result.json', 'summary.json')
DEFAULT_ARCHIVE = Path(os.environ.get('AGENT_SDK_TESTIFY_DATA_DIR') or
                       str(Path(os.environ.get('XDG_DATA_HOME') or str(Path.home() / '.local/share')) /
                           'agent-sdk-testify')) / 'archives'


def check(condition, message):
    if not condition:
        raise ValueError(message)


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def read(path):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            check(key not in result, 'duplicate JSON evidence key: ' + key)
            result[key] = value
        return result
    return json.loads(Path(path).read_text(), object_pairs_hook=unique)


def write(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('x') as stream:
        json.dump(value, stream, indent=2, ensure_ascii=False)
        stream.write('\n')
    path.chmod(0o600)


def local(name):
    p = PurePosixPath(name)
    check(bool(name) and not p.is_absolute() and '..' not in p.parts and str(p) == name,
          'non-local or non-canonical material path')
    return p


def regular(root, name):
    local(name)
    p = Path(root)
    for part in PurePosixPath(name).parts:
        p = p / part
        check(not p.is_symlink(), 'material symlink is forbidden: ' + name)
    check(p.is_file(), 'required regular material missing: ' + name)
    return p


def inventory(root, *, allow_empty=False):
    root = Path(root)
    check(root.is_dir() and not root.is_symlink(), 'material directory is missing or linked')
    files = {}
    for p in sorted(root.rglob('*')):
        check(not p.is_symlink(), 'material symlink is forbidden')
        if p.is_dir():
            continue
        name = p.relative_to(root).as_posix()
        files[name] = digest(regular(root, name))
    check(allow_empty or bool(files), 'empty material inventory')
    return files


def verify_hashes(root, hashes):
    check(isinstance(hashes, dict) and bool(hashes), 'nonempty hash inventory required')
    for name, expected in hashes.items():
        check(isinstance(expected, str) and len(expected) == 64 and digest(regular(root, name)) == expected,
              'material hash mismatch: ' + name)


def catalog():
    result = {}
    for story in read(SCENARIOS / 'index.json')['stories']:
        for case in read(SCENARIOS / (story + '.json'))['cases']:
            for provider in ('codex', 'claude'):
                binding = read(SCENARIOS / 'bindings' / (provider + '.json'))['cases'][case['id']]
                route = dict(case['execution'], **binding.get('execution', {}))
                mode = route['mode']
                result[(provider, case['id'])] = (story, 'required' if mode == 'live-selected-path' else mode)
    return result


def load_index(path):
    index = read(path)
    check(index.get('version') == 1, 'unsupported corpus index version')
    expected, seen = catalog(), set()
    for entry in index['cases']:
        key = (entry['provider'], entry['caseId'])
        check(key in expected and key not in seen, 'unknown or duplicate corpus identity')
        seen.add(key)
        check((entry['storyId'], entry['requirement']) == expected[key], 'corpus requirement differs from catalog')
        check(entry.get('adoption', 'missing') in ('missing', 'untriggered', 'admitted'), 'invalid adoption state')
        check(entry.get('adoption') != 'admitted' or entry['requirement'] == 'required', 'unavailable case cannot be admitted')
    check(seen == set(expected) and bool(seen), 'corpus index omits required identities')
    return index


def validate_publish_path(name):
    p = local(name)
    top = {'plan.json', 'manifest.json', 'summary.json', 'result.json', 'report.json', 'remote-service.json'}
    allowed = name in top or (p.parts[0] in ('raw', 'history-seed', 'driver') and len(p.parts) > 1)
    allowed |= name.startswith(('config/projects/', 'config/sessions/', 'remote-probe/raw/'))
    check(allowed and not any(x in name.lower() for x in ('credentials', 'auth.json', '.private', '.env')),
          'file is not publishable regression material: ' + name)


def source_plan(root):
    return read(regular(root, 'plan.json'))


def identity(plan):
    return plan['provider'], plan.get('storyId', plan.get('story')), plan['caseId']


def phases(plan, mode):
    out = [('', '')]
    if plan.get('driverProfile') == 'remote-close':
        out.append(('remote-probe/', 'remote-probe/'))
    if mode == 'simulate' and plan.get('scenario', {}).get('parameters', {}).get('childDetailPolicy'):
        out.append(('suppress/', ''))
    return out


def validate_summary(root, mode, plan):
    s = read(regular(root, 'summary.json'))
    check(s.get('mode') == mode and s.get('provider') == plan['provider'] and s.get('case') == plan['caseId'],
          'run identity or mode differs')
    check(not s.get('error') and s.get('result', {}).get('status') == 'passed', 'run did not pass')
    check(read(regular(root, 'result.json')).get('status') == 'passed', 'business result did not pass')
    return s


def validate_replay(source, replay, plan, mode='replay'):
    s = validate_summary(replay, mode, plan)
    check(s.get('source') == {n: digest(regular(source, n)) for n in SOURCE_EVIDENCE},
          'run does not bind the original source plan and manifest; rerun official replay')
    for receipt_prefix, raw_prefix in phases(plan, mode):
        raw = regular(source, raw_prefix + 'raw/raw.jsonl')
        r = read(regular(replay, receipt_prefix + 'replay-peer.json'))
        check(r.get('provider') == plan['provider'] and r.get('recordingSHA256') == digest(raw)
              and r.get('fullConsumption') is True and r.get('clientEOF') is True
              and r.get('nativeLaunched') is False and not r.get('error'), 'incomplete or incorrect mock receipt')
        check(r.get('frames') == len(raw.read_bytes().splitlines()), 'receipt frame count differs')
    check(s.get('recordingSHA256') == digest(regular(source, 'raw/raw.jsonl')), 'summary recording hash differs')
    if plan['provider'] == 'claude':
        materialization = regular(replay, 'materialization.json')
        check(digest(materialization) == s.get('materializationSHA256'), 'runtime materialization hash differs')
        m = read(materialization)
        execution = m['executionProject']
        check(execution == str(Path(s['output']) / 'project') and m.get('originalProject') == plan['project']
              and m.get('originalHistoryProject') == plan.get('historyProject'), 'runtime project mapping differs')
        for change in m['transformations'] or []:
            check(change['kind'] == 'claude-project-cwd-copy' and change['sourceCWD'] in
                  (plan.get('project'), plan.get('historyProject')) and change['executionCWD'] == execution,
                  'unsupported runtime transformation')
            prefix = 'config/projects/' + re.sub('[^a-zA-Z0-9]', '-', change['sourceCWD']) + '/'
            target = 'config/projects/' + re.sub('[^a-zA-Z0-9]', '-', execution) + '/'
            check(change['source'].startswith(prefix) and change['derived'] == target + change['source'][len(prefix):],
                  'runtime history ownership differs')
            verify_hashes(source, {change['source']: change['sourceSHA256']})
            verify_hashes(replay, {change['derived']: change['derivedSHA256']})


def replay_evidence(replay, plan):
    names = {'summary.json', 'result.json', 'replay-report.json',
             *(p + 'replay-peer.json' for p, _ in phases(plan, 'replay'))}
    if plan['provider'] == 'claude':
        names.add('materialization.json')
        names.update(c['derived'] for c in read(regular(replay, 'materialization.json'))['transformations'] or [])
    return names


def required_materials(source, plan):
    names = {'plan.json', 'manifest.json', 'summary.json', 'result.json', 'report.json'}
    manifest = read(regular(source, 'manifest.json'))
    for group in ('savedFiles', 'caseFiles', 'remotePhaseFiles', 'driverFiles'):
        hashes = manifest.get(group, {})
        if hashes:
            verify_hashes(source, hashes)
            names.update(hashes)
    for _, prefix in phases(plan, 'replay'):
        names.update(prefix + 'raw/' + n for n in ('raw.jsonl', 'manifest.json', 'transport.json', 'stderr.raw'))
    # All saved history copies are execution inputs, including old manifests that
    # predate the history-seed group. They must be reviewed, never rediscovered.
    for prefix in ('config/projects', 'config/sessions', 'history-seed'):
        directory = Path(source) / prefix
        if directory.exists():
            names.update(prefix + '/' + n for n in inventory(directory, allow_empty=True))
    for name, evidence in plan.get('historySeed', {}).get('files', {}).items():
        verify_hashes(source, {name: evidence['sha256']})
        names.add(name)
    if plan.get('driverProfile') == 'remote-close':
        names.add('remote-service.json')
    return names


def validate_admission(source, replay, review):
    check(review.get('version') == 1 and review.get('origin') == 'real-agent', 'review must attest real Agent origin')
    for field in ('reviewer', 'reviewedAt'):
        check(isinstance(review.get(field), str) and bool(review[field].strip()), 'review field required: ' + field)
    check(datetime.fromisoformat(review['reviewedAt'].replace('Z', '+00:00')).tzinfo is not None,
          'review time must include a timezone')
    for field in ('sourceReviewed', 'contentReviewed', 'credentialsReviewed'):
        check(review.get(field) is True, 'explicit review required: ' + field)
    check(inventory(source) == review.get('captureFiles'), 'capture inventory drifted or is incomplete')
    verify_hashes(replay, review.get('replayFiles'))
    plan = source_plan(source)
    provider, story, case = identity(plan)
    check(catalog().get((provider, case)) == (story, 'required'), 'unknown, unavailable or wrongly owned case')
    definition = next(c for c in read(SCENARIOS / (story + '.json'))['cases'] if c['id'] == case)
    check(plan.get('driverProfile') == definition['driverProfile'], 'capture driver profile differs from case')
    manifest = read(regular(source, 'manifest.json'))
    check(manifest.get('provenance') == 'capture' and manifest.get('status') == 'passed'
          and manifest.get('captureStatus') == 'complete' and manifest.get('credentialScan') == 'pass',
          'real source capture, business pass and credential scan required')
    check(manifest.get('planSHA256') == digest(regular(source, 'plan.json')), 'source plan hash differs')
    summary = validate_summary(source, 'record', plan)
    check(summary.get('recordingSHA256') == digest(regular(source, 'raw/raw.jsonl')), 'capture summary hash differs')
    sdk_package = {'codex': 'openai-codex', 'claude': 'claude-agent-sdk'}[provider]
    for root, name in ((source, 'report.json'), (replay, 'replay-report.json')):
        business = read(regular(root, name))
        sdk = business.get('officialSDK', {})
        check(business.get('status') == 'passed' and business.get('branchStatus') == 'passed'
              and business.get('captureStatus') == 'complete' and not business.get('gaps')
              and business.get('provider') == provider and business.get('caseId') == case
              and business.get('storyId') == story
              and sdk.get('package') == sdk_package and bool(sdk.get('version')),
              'official SDK business evidence is absent, untriggered or incomplete')
        if name == 'report.json':
            check(sdk == review.get('officialSDK'), 'review SDK identity differs from capture')
    for _, prefix in phases(plan, 'replay'):
        rawroot = Path(source) / prefix / 'raw'
        m = read(regular(rawroot, 'manifest.json'))
        t = read(regular(rawroot, 'transport.json'))
        check(m.get('recordingFormat') == {'name': 'agent-mock-jsonl', 'version': 1, 'transport': 'stdio-jsonl'}
              and m.get('captureStatus') == 'complete', 'unsupported or incomplete capture format')
        native = m.get('native', {})
        expected_cwd = plan.get('historyProject') if plan.get('driverProfile') == 'existing-children' else plan.get('project')
        check(bool(native.get('executable')) and re.fullmatch('[0-9a-f]{64}', native.get('sha256', ''))
              and bool(expected_cwd) and native.get('cwd') == expected_cwd,
              'observed native execution identity required')
        verify_hashes(rawroot, m.get('files'))
        for name in ('raw.jsonl', 'stderr.raw', 'transport.json'):
            check(name in m['files'], 'raw manifest omits required artifact')
        check(t.get('captureStatus') == 'complete' and t.get('cleanupComplete') is True
              and t.get('nativeLaunched') is True and t.get('reaped') is True
              and t.get('clientEOF') is True and not t.get('error'), 'capture or cleanup incomplete')
        rows = [json.loads(line) for line in regular(rawroot, 'raw.jsonl').read_bytes().splitlines()]
        check(bool(rows) and t.get('frames') == len(rows), 'capture frame count differs')
        for n, row in enumerate(rows, 1):
            check(set(row) == {'ordinal', 'direction', 'channel', 'epoch', 'elapsedMs', 'wire'}
                  and row['ordinal'] == n and row['epoch'] == 1 and row['channel'] == 'protocol'
                  and row['direction'] in ('c2s', 's2c') and isinstance(row['wire'], dict),
                  'only original capture JSONL envelopes may be admitted')
    verify_hashes(Path(source) / 'raw', manifest.get('files'))
    if plan.get('driverProfile') == 'remote-close':
        service = read(regular(source, 'remote-service.json'))
        check(service.get('cleanupComplete') is True and service.get('serverExitCode') == 0
              and service.get('forcedCleanup') is False, 'remote source cleanup incomplete')
    validate_replay(source, replay, plan)
    required = required_materials(source, plan)
    selected = review.get('publishFiles', [])
    check(isinstance(selected, list) and len(set(selected)) == len(selected) and required <= set(selected),
          'review omits reusable materials')
    for name in selected:
        validate_publish_path(name)
        regular(source, name)
    for name in ('summary.json', 'result.json', *(p + 'replay-peer.json' for p, _ in phases(plan, 'replay'))):
        check(name in review['replayFiles'], 'review omits official replay evidence')
    check(set(review['replayFiles']) == replay_evidence(replay, plan), 'review must select only official replay evidence files')
    return plan


def copy_selected(source, target, names):
    for name in names:
        src = regular(source, name)
        dest = Path(target) / name
        dest.parent.mkdir(parents=True, exist_ok=True)
        with dest.open('xb') as stream:
            stream.write(src.read_bytes())
        dest.chmod(0o600)


def new_attempt(base):
    path = Path(base) / (datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ-') + uuid.uuid4().hex)
    path.mkdir(parents=True, mode=0o700)
    return path


def adopt(source, replay, review_path, index_path, archive):
    source, replay = Path(source).resolve(), Path(replay).resolve()
    index_path = Path(index_path).resolve()
    archive = Path(archive).resolve()
    # Publication is allowlisted; originals, including failed attempts and
    # unselected private diagnostics, always survive in the ignored private area.
    check(not archive.is_relative_to(index_path.parent), 'private archive must be outside the regression corpus')
    check(not archive.is_relative_to(ROOT) or archive.is_relative_to(ROOT / '.artifacts'),
          'in-checkout private archives must be under ignored .artifacts')
    check(not archive.is_relative_to(source), 'archive cannot be inside source capture')
    original_hashes = inventory(source)
    saved = new_attempt(archive)
    copy_selected(source, saved / 'capture', original_hashes)
    write(saved / 'original-files.json', original_hashes)
    shutil.copyfile(review_path, saved / 'review.json')
    try:
        review = read(review_path)
        verify_hashes(replay, review.get('replayFiles'))
        copy_selected(replay, saved / 'official-replay', review['replayFiles'])
        plan = validate_admission(saved / 'capture', saved / 'official-replay', review)
        check(inventory(source) == original_hashes, 'capture changed during adoption')
        lock = index_path.with_suffix('.lock')
        fd = os.open(lock, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        try:
            index = load_index(index_path)
            provider, story, case = identity(plan)
            entry = next(e for e in index['cases'] if (e['provider'], e['caseId']) == (provider, case))
            check(entry.get('adoption') != 'admitted', 'case already admitted; adoption never replaces a fixed sample')
            name = f'{provider}/{story}/{case}/{saved.name}'
            target = index_path.parent / name
            target.mkdir(parents=True, mode=0o700)
            copy_selected(saved / 'capture', target, review['publishFiles'])
            copy_selected(saved / 'official-replay', target / 'official-replay', review['replayFiles'])
            write(target / 'review.json', review)
            # No transformation of frames, native cwd, IDs, or saved history.
            record = {'version': 1, 'adoption': 'admitted', 'archiveId': saved.name,
                      'originalFiles': original_hashes, 'transformations': [], 'files': inventory(target)}
            write(target / 'adoption.json', record)
            entry.update(adoption='admitted', materials=name, adoptionSHA256=digest(target / 'adoption.json'))
            replacement = index_path.with_name(index_path.name + '.' + uuid.uuid4().hex)
            write(replacement, index)
            replacement.replace(index_path)
        finally:
            os.close(fd)
            lock.unlink()
        write(saved / 'adoption-result.json', {'status': 'admitted', 'materials': name})
        return target
    except Exception:
        write(saved / 'adoption-result.json', {'status': 'rejected'})
        raise


def verify_adopted(index_path, entry):
    local(entry['materials'])
    root = Path(index_path).resolve().parent / entry['materials']
    regular(Path(index_path).resolve().parent, entry['materials'] + '/adoption.json')
    expected_prefix = '/'.join((entry['provider'], entry['storyId'], entry['caseId'])) + '/'
    check(entry['materials'].startswith(expected_prefix), 'material ownership path differs')
    check(digest(regular(root, 'adoption.json')) == entry['adoptionSHA256'], 'adoption hash drift')
    adoption = read(root / 'adoption.json')
    check(adoption.get('adoption') == 'admitted' and adoption.get('transformations') == [], 'unsupported adoption record')
    files = inventory(root)
    del files['adoption.json']
    check(files == adoption['files'], 'adopted corpus inventory drift')
    review = read(regular(root, 'review.json'))
    # CaptureFiles describes the complete private original; the reviewed subset
    # must preserve the original hashes. Admission evidence is rechecked offline.
    original = review['captureFiles']
    check(original == adoption['originalFiles'], 'original provenance hash inventory drift')
    verify_hashes(root, {n: original[n] for n in review['publishFiles']})
    reduced = dict(review, captureFiles={n: h for n, h in inventory(root).items()
                                       if n in review['publishFiles']})
    # Validate a view containing only original files, without copying unknown
    # private data into the execution corpus or requiring the old source tree.
    view = new_attempt(ROOT / '.artifacts/simulation/validation')
    try:
        copy_selected(root, view, review['publishFiles'])
        plan = validate_admission(view, root / 'official-replay', reduced)
    finally:
        shutil.rmtree(view)
    check(identity(plan) == (entry['provider'], entry['storyId'], entry['caseId']), 'adopted identity differs')
    return root


def gate(index_path, output=None, jobs=2):
    out = Path(output).resolve() if output else new_attempt(ROOT / '.artifacts/simulation/gates')
    if output:
        out.mkdir(parents=True, mode=0o700)
    report = {'version': 1, 'cases': [], 'status': 'failed'}
    try:
        report['indexSHA256'] = digest(index_path)
        build = out / 'bin'
        build.mkdir()
        for binary, package in (('agent-sdk-testify', './tools/agent-sdk-testify'), ('sdk-driver', './tests/simulation/drivers')):
            with (out / (binary + '-build.log')).open('xb') as log:
                subprocess.run(['mise', 'exec', '--', 'go', 'build', '-o', str(build / binary), package],
                               cwd=ROOT, stdout=log, stderr=subprocess.STDOUT, check=True)
        report['binaries'] = {p.name: digest(p) for p in build.iterdir()}
        report['sourceRevision'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
        report['sourceFiles'] = {str(p.relative_to(ROOT)): digest(p) for p in sorted(ROOT.rglob('*.go'))
                                 if '.artifacts' not in p.parts}
        for name in ('go.mod', 'go.sum', 'mise.toml'):
            report['sourceFiles'][name] = digest(ROOT / name)
        index = load_index(index_path)
        selected = []
        for entry in index['cases']:
            row = {k: entry[k] for k in ('provider', 'storyId', 'caseId', 'requirement')}
            row['adoption'] = entry.get('adoption', 'missing')
            row['status'] = entry['requirement'] if entry['requirement'] != 'required' else row['adoption']
            if row['adoption'] == 'admitted':
                try:
                    material = verify_adopted(index_path, entry)
                    selected.append({'case': str(material), 'provider': entry['provider'], 'caseId': entry['caseId']})
                    row['status'] = 'pending'
                except (ValueError, OSError, KeyError, TypeError) as error:
                    row.update(status='failed', error=str(error))
            report['cases'].append(row)
        if selected:
            write(out / 'cases.json', selected)
            with (out / 'simulate.log').open('xb') as log:
                completed = subprocess.run([str(build / 'agent-sdk-testify'), 'simulate', '--cases-file', str(out / 'cases.json'),
                                           '--driver', str(build / 'sdk-driver'), '--jobs', str(jobs),
                                           '--output', str(out / 'runs')], cwd=ROOT, stdout=log, stderr=subprocess.STDOUT)
            report['simulateExitCode'] = completed.returncode
            for row in report['cases']:
                if row['status'] != 'pending':
                    continue
                item = next(x for x in selected if x['provider'] == row['provider'] and x['caseId'] == row['caseId'])
                material = Path(item['case'])
                try:
                    validate_replay(material, out / 'runs' / (row['provider'] + '-' + row['caseId']), source_plan(material), 'simulate')
                    row['status'] = 'passed'
                except (ValueError, OSError, KeyError, TypeError) as error:
                    row.update(status='failed', error=str(error))
        required = [r for r in report['cases'] if r['requirement'] == 'required']
        if required and all(r['status'] == 'passed' for r in required) and report.get('simulateExitCode') == 0:
            report['status'] = 'passed'
    except (ValueError, OSError, KeyError, TypeError, subprocess.SubprocessError) as error:
        report['error'] = str(error)
    report['counts'] = dict(Counter(row['status'] for row in report['cases']))
    write(out / 'coverage.json', report)
    print(json.dumps({'status': report['status'], 'counts': report['counts'], 'report': str(out / 'coverage.json')}))
    return report['status'] == 'passed'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    for name in ('review', 'adopt'):
        p = commands.add_parser(name)
        p.add_argument('--source', type=Path, required=True)
        p.add_argument('--replay', type=Path, required=True)
        p.add_argument('--review', type=Path, required=True)
        if name == 'adopt':
            p.add_argument('--index', type=Path, default=DEFAULT_INDEX)
            p.add_argument('--archive', type=Path, default=DEFAULT_ARCHIVE)
    p = commands.add_parser('gate')
    p.add_argument('--index', type=Path, default=DEFAULT_INDEX)
    p.add_argument('--output', type=Path)
    p.add_argument('--jobs', type=int, default=2)
    args = parser.parse_args()
    try:
        if args.command == 'gate':
            check(args.jobs > 0, 'jobs must be positive')
            return 0 if gate(args.index, args.output, args.jobs) else 1
        if args.command == 'review':
            plan = source_plan(args.source)
            replay_files = sorted(replay_evidence(args.replay, plan))
            write(args.review, {'version': 1, 'origin': 'unreviewed', 'reviewer': '', 'reviewedAt': '',
                                'officialSDK': read(args.source / 'report.json').get('officialSDK'),
                                'sourceReviewed': False, 'contentReviewed': False, 'credentialsReviewed': False,
                                'captureFiles': inventory(args.source),
                                'replayFiles': {n: digest(regular(args.replay, n)) for n in replay_files},
                                'publishFiles': sorted(required_materials(args.source, plan))})
        else:
            print(adopt(args.source, args.replay, args.review, args.index, args.archive))
        return 0
    except (ValueError, OSError, KeyError, TypeError) as error:
        print(str(error), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
