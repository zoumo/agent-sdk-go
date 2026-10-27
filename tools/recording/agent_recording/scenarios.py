"""Interpret language-neutral JSON scenarios. No native SDK imports."""
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import sys


def scenario_directory():
    checkout = Path(__file__).parent.parent / 'scenarios'
    if checkout.is_dir():
        return checkout
    return Path(sys.prefix) / 'share/agent-sdk-protocol-recording/scenarios'


def scenario_assets(directory=None):
    """Files needed to consume the configuration without the source checkout."""
    root = directory or scenario_directory()
    return sorted(path for path in root.rglob('*.json') if path.is_file())


def _unique(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError('duplicate scenario JSON key: ' + key)
        value[key] = item
    return value


def _read(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_size > 1 << 20:
            raise ValueError('scenario must be a bounded regular JSON file')
        return json.loads(os.read(fd, (1 << 20) + 1), object_pairs_hook=_unique,
                          parse_constant=lambda value: (_ for _ in ()).throw(ValueError('non-JSON constant: ' + value)))
    finally:
        os.close(fd)


def _validate(value, schema, path='$'):
    """Validate the JSON Schema keywords used by our portable schema files.

    The files are also consumable by standard Draft 2020-12 validators. No
    language-specific expressions or executable configuration are supported.
    """
    kinds = {'object': dict, 'array': list, 'string': str, 'integer': int, 'null': type(None)}
    expected = schema.get('type')
    if expected:
        choices = expected if isinstance(expected, list) else [expected]
        if not any(type(value) is kinds[kind] for kind in choices):
            raise ValueError(path + ': invalid JSON type')
    if 'const' in schema and (type(value) is not type(schema['const']) or value != schema['const']):
        raise ValueError(path + ': unsupported schema version or constant')
    if 'enum' in schema and value not in schema['enum']:
        raise ValueError(path + ': unsupported value')
    if 'minimum' in schema and value < schema['minimum']:
        raise ValueError(path + ': below minimum')
    if 'maximum' in schema and value > schema['maximum']:
        raise ValueError(path + ': above maximum')
    if isinstance(value, dict):
        if set(schema.get('required', [])) - value.keys():
            raise ValueError(path + ': missing fields')
        properties = schema.get('properties', {})
        additional = schema.get('additionalProperties', True)
        for name, item in value.items():
            if name in properties:
                _validate(item, properties[name], path + '.' + name)
            elif additional is False:
                raise ValueError(path + ': unknown field ' + name)
            elif isinstance(additional, dict):
                _validate(item, additional, path + '.' + name)
    if isinstance(value, list):
        if len(value) < schema.get('minItems', 0):
            raise ValueError(path + ': empty selection')
        if schema.get('uniqueItems') and len({json.dumps(item, sort_keys=True) for item in value}) != len(value):
            raise ValueError(path + ': duplicate entries')
        for index, item in enumerate(value):
            _validate(item, schema.get('items', {}), path + '[' + str(index) + ']')


def _document(directory, filename, schema):
    value = _read(directory / filename)
    _validate(value, _read(directory / (schema + '.schema.json')))
    return value


def story_ids(directory=None):
    directory = directory or scenario_directory()
    values = _document(directory, 'index.json', 'index')['scenarios']
    if not all(re.fullmatch(r'SDK-US-\d{3}', value) for value in values):
        raise ValueError('invalid scenario identity')
    return values


STORIES = story_ids()


def _workflow(core):
    """Compile the supported declarative workflow forms, independently of IDs."""
    steps = core['steps']
    if not steps:
        if core['inputs']:
            raise ValueError('inputs without workflow')
        return [], 'sequential-results', None
    submitted = [step['input'] for step in steps if step['op'] == 'input.submit' and 'input' in step]
    if len(set(submitted)) != len(submitted) or set(submitted) != core['inputs'].keys():
        raise ValueError('workflow must reference each declared input exactly once')
    opening = [{'op': 'session.open'}]
    if {'op': 'configuration.apply'} in steps:
        opening.append({'op': 'configuration.apply'})
    result = {'op': 'event.wait', 'event': 'execution.result', 'onMissing': 'untriggered'}
    closing = [{'op': 'session.close'}]
    progress = {'op': 'event.wait', 'event': 'execution.progress', 'onMissing': 'untriggered'}
    if progress in steps:
        if len(submitted) not in (1, 2):
            raise ValueError('unsupported progress workflow')
        action = {'op': 'execution.interrupt'} if len(submitted) == 1 else {'op': 'input.submit', 'input': submitted[1]}
        expected = opening + [{'op': 'input.submit', 'input': submitted[0]}, progress, action, result] + closing
        trigger = 'interrupt' if len(submitted) == 1 else 'submit-supplement'
        submission = 'progress-barrier' if len(submitted) == 2 else 'sequential-results'
    else:
        expected = opening + [step for name in submitted for step in ({'op': 'input.submit', 'input': name}, result)] + closing
        submission, trigger = 'sequential-results', None
    if steps != expected:
        raise ValueError('unsupported workflow order or fields')
    if core['limits']['inputs'] != len(submitted):
        raise ValueError('workflow input limit differs from declared submissions')
    return submitted, submission, trigger


def recipe(provider, story, directory=None):
    directory = directory or scenario_directory()
    if not re.fullmatch(r'[a-z][a-z0-9-]*', provider) or story not in story_ids(directory):
        raise ValueError('unknown provider or story')
    core = _document(directory, story + '.json', 'scenario')
    binding_file = _document(directory, 'bindings/' + provider + '.json', 'bindings')
    if core['id'] != story or binding_file['provider'] != provider:
        raise ValueError('scenario or binding identity mismatch')
    if core['expect']['caseId'] != story + '/' + core['name'] or not core['expect']['scope'].strip():
        raise ValueError('case identity and scope must be explicit')
    if any(not value.strip() for value in core['expect']['outOfScope']):
        raise ValueError('empty case exclusion')
    binding = binding_file['scenarios'][story]
    submitted, submission, trigger = _workflow(core)
    execution = dict(core['execution'], **binding.get('execution', {}))
    mode = execution['mode']
    prompts = binding.get('inputs', [core['inputs'][name] for name in submitted]) if mode == 'live-selected-path' else []
    if mode == 'live-selected-path' and (not prompts or len(prompts) != len(submitted)):
        raise ValueError('provider inputs differ from workflow submissions')
    if bool(binding['configuration']) != ({'op': 'configuration.apply'} in core['steps']):
        raise ValueError('configuration binding differs from workflow')
    configuration = binding['configuration']
    if configuration and set(configuration) != {'timing', 'modelSource', 'thinking', 'effectiveValue'}:
        raise ValueError('incomplete configuration binding')
    if configuration and ((provider == 'codex' and configuration['modelSource'] != 'session') or
                          (provider == 'claude' and (configuration['modelSource'] != 'prepared-or-default' or configuration['thinking'] is not None))):
        raise ValueError('configuration operation unsupported by Python adapter')
    answers = dict(core['expect'].get('answers', {}), **binding['answers'])
    if 'question.answer' in core['expect']['requests']:
        if not isinstance(answers.get('choice'), str) or not isinstance(answers.get('options'), list) or answers['choice'] not in answers['options']:
            raise ValueError('question answer is not a configured option')
    files = dict(core['setup']['files'], **binding['files'])
    for name in files:
        path = PurePosixPath(name)
        if not name or path.is_absolute() or '..' in path.parts or '\\' in name or str(path) != name:
            raise ValueError('scenario file escapes project')
    requests = binding['requests']
    if set(requests) != set(core['expect']['requests']):
        raise ValueError('native request bindings differ from expected interactions')
    limits = dict(core['limits'], inputs=len(prompts))
    return {'version': core['version'], 'provider': provider, 'story': story,
            'bundle': core['name'], 'mode': mode, 'reason': execution['reason'],
            'goals': core['goals'], 'nativeStories': binding['nativeStories'],
            'prompts': prompts, 'files': files, 'submission': submission,
            'progressAction': trigger, 'requiredRequests': [requests[name] for name in core['expect']['requests']],
            'progressBarrier': binding['progressBarrier'], 'answers': answers,
            'configuration': binding['configuration'], 'steps': core['steps'] if prompts else [],
            'offlineTest': binding['offlineTest'], 'limits': limits, 'scope': core['expect']['scope'],
            'case': {'id': core['expect']['caseId'], 'scope': core['expect']['scope'],
                     'outOfScope': core['expect']['outOfScope']}}


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def validate_recipe(plan):
    expected = recipe(plan['provider'], plan['story'])
    if plan.get('scenario') != expected or plan.get('scenarioSHA256') != digest(expected):
        raise ValueError('scenario definition changed or unsupported')
    if plan['prompts'] != expected['prompts']:
        raise ValueError('scenario prompts changed')
    return expected


class Untriggered(ValueError):
    """Required native evidence was absent; do not retry to force it."""


class Flow:
    """Own only recording steps; native SDKs keep requests and answer authority."""
    def __init__(self, definition):
        self.definition = definition
        self.trace = []
        self.sent = 0
        self.completed = 0
        self.active = False
        self.triggered = False
        self.requests = []

    def inputs(self):
        prompts = self.definition['prompts']
        return prompts[:1] if self.definition['submission'] == 'progress-barrier' else prompts

    def submit(self):
        if self.active or self.sent >= len(self.definition['prompts']):
            raise ValueError('input outside scenario order/budget')
        self.trace.append({'step': 'submit', 'input': self.sent})
        self.sent += 1
        self.active = True

    def progress(self):
        action = self.definition['progressAction']
        if not self.active or not action or self.triggered:
            return None
        self.triggered = True
        self.trace.append({'step': 'progress-barrier', 'action': action})
        return action

    def action(self, action, outcome):
        if not self.active or not self.triggered or action != self.definition['progressAction']:
            raise ValueError('action without native progress barrier')
        if any(item.get('step') == action for item in self.trace):
            raise ValueError('scenario action repeated')
        if outcome not in ('accepted', 'rejected', 'written'):
            raise ValueError('unknown native action outcome')
        if action == 'submit-supplement':
            if self.sent >= len(self.definition['prompts']):
                raise ValueError('supplement input budget exhausted')
            self.sent += 1
        self.trace.append({'step': action, 'outcome': outcome})

    def request(self, name):
        if not self.active or name not in self.definition['requiredRequests'] or name in self.requests:
            raise ValueError('request outside scenario order/policy')
        expected = self.definition['requiredRequests'][len(self.requests)]
        if name != expected:
            raise ValueError('native request order changed')
        self.requests.append(name)
        self.trace.append({'step': 'request', 'name': name})

    def result(self):
        if not self.active:
            raise ValueError('result without submitted input')
        self.completed += 1
        self.active = False
        self.trace.append({'step': 'result', 'input': self.completed - 1})

    def finish(self):
        missing = []
        if self.definition['progressAction'] and not any(
                item['step'] == self.definition['progressAction'] for item in self.trace):
            missing.append(self.definition['progressAction'])
        missing.extend(name for name in self.definition['requiredRequests'] if name not in self.requests)
        # A supplemental native request can be rejected, or can share an existing
        # execution. Do not invent an extra result boundary for that input.
        required_results = len(self.inputs())
        if self.active or self.completed != required_results:
            missing.append('result-boundaries')
        if missing:
            raise Untriggered('required scenario evidence absent: ' + ', '.join(missing))
        self.trace.append({'step': 'close-drain'})
