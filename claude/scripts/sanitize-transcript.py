#!/usr/bin/env python3
"""Fail-closed publisher for this synthetic capture schema; never prints raw values."""
import argparse, hashlib, json, pathlib, re, collections, datetime

KEYS=set('type subtype request_id request response hooks forwardSubagentText message role content session_id parent_tool_use_id isReplay uuid timestamp id model stop_reason stop_sequence usage input_tokens output_tokens cache_creation_input_tokens cache_read_input_tokens cache_creation ephemeral_5m_input_tokens ephemeral_1h_input_tokens server_tool_use web_search_requests web_fetch_requests service_tier inference_geo iterations speed name input text thinking signature tool_use_id tool_use_result file_path offset limit pages file content numLines startLine totalLines originalFile structuredPatch userModified status result is_error num_turns duration_ms duration_api_ms total_cost_usd modelUsage permission_denials permissionMode cwd tools mcp_servers apiKeySource claude_code_version output_style agents skills plugins commands slash_commands fast_mode_state event index delta content_block text_delta thinking_delta signature_delta partial_json message_start message_delta message_stop content_block_start content_block_delta content_block_stop state reason tool_name decision_reason blocked_path permission_suggestions title display_name description questions question header options label multiSelect answers behavior updatedInput toolUseID toolUseId agentId agent_id task_id data toolUseResult output tokens version account subscriptionType email organization apiKeyOrigin auth_method maxOutputTokens maxOutputTokensOverride output_tokens_details reasoning_tokens budget_tokens stopReason inputTokens outputTokens cacheReadInputTokens cacheCreationInputTokens webSearchRequests costUSD contextWindow maxOutputTokens tool_input start_time end_time summary source parent_task_id permissionMode available_models supported_commands supported_models capabilities fast_mode commands description argumentHint argument_hint value supportsEffort supportedEffortLevels supportsAdaptiveThinking supportsFastMode displayName error errors elapsed_time_ms rate_limit_info status resetsAt rateLimitType utilization overageStatus overageResetsAt overageDisabledReason isUsingOverage content_type filePath success typeName oldString newString replaceAll structured_patch total_lines lines added removed'.split())
SECRET=re.compile(r'(?i)(sk-(?:ant-)?[a-z0-9_-]{12,}|bearer[ :]+[a-z0-9._-]{8,}|authorization|access_token|refresh_token|api[_-]?key[" ]*[:=]|/users/|/home/|[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,})')
KEYS.update('apiProvider api_error_status available_output_styles background budget by_type completed concurrency_limit container context_management current_permission_mode depth_limit diagnostics displayName exit_code failed fast_mode_disabled_reason foreground hook_event hook_id hook_name ide_rc_auto_enable_gate is_api_error_message killed max_depth messaging_socket_path models outcome output output_tokens_details parent pid product_feedback_disabled queued_turn_count refused remote_control_auto_enable remote_control_auto_on_by_default remote_control_available requested resolvedModel session_state spawned spawned_by_subagents started_in_background stderr stdout stop_details subagent_stats supportsAutoMode system terminal_reason thinking_tokens tokenSource unset user analytics_disabled'.split())
IDKEYS={'hook_id','id','uuid','session_id','request_id','tool_use_id','parent_tool_use_id','agentId','agent_id','task_id','toolUseID','toolUseId'}
SUPPORTED_VERSION = '2.1.258'
# Any future CLI needs its own reviewed schema, never a minimum-version range.
KEYS.update('max_thinking_tokens thinking_display'.split())
VERSION_KEYS = {SUPPORTED_VERSION: frozenset(KEYS)}
def strict_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result: raise ValueError('duplicate JSON key quarantined')
        result[key] = value
    return result

def publish(raw,root,cli_version=SUPPORTED_VERSION):
    if cli_version not in VERSION_KEYS: raise ValueError('unreviewed CLI version quarantined')
    allowed = VERSION_KEYS[cli_version]
    frames=[json.loads(x, object_pairs_hook=strict_object) for x in raw.splitlines()]
    ids={}; refs=collections.defaultdict(list); namespaces={}; timebase=[]
    def collect(v,field='',ordinal=0):
        if isinstance(v,dict):
            if 'claude_code_version' in v and v['claude_code_version'] != cli_version:
                raise ValueError('native source version mismatch quarantined')
            # The root allowlist is not a reviewed child/storage schema. Keep
            # raw task/alias/permission material private until the child profile
            # has independent per-version publication and equality review.
            if v.get('subtype') in ('task_started', 'task_progress', 'task_updated', 'task_notification', 'background_tasks_changed') or any(
                v.get(key) not in (None, '') for key in ('agent_id', 'agentId', 'parent_tool_use_id', 'parent_task_id', 'parentAgentId')
            ):
                raise ValueError('unreviewed child publication profile quarantined')
            for k,x in v.items():
                if k in IDKEYS and isinstance(x,(str,int)) and x!='':
                    key=(type(x).__name__,x)
                    ns = ('tool' if k in ('tool_use_id','parent_tool_use_id','toolUseID','toolUseId') or (k=='id' and v.get('type')=='tool_use') else 'message' if k=='id' else 'agent' if k in ('agent_id','agentId') else k)
                    if key in namespaces and namespaces[key]!=ns:raise ValueError('ambiguous ID namespace quarantined')
                    namespaces[key]=ns
                    if key not in ids: ids[key]=('fixture-id-%03d'% (len(ids)+1)) if isinstance(x,str) else 100000+len(ids)
                    refs[key].append({'ordinal':ordinal,'field':k})
                collect(x,k,ordinal)
        elif isinstance(v,list):
            for x in v:collect(x,field,ordinal)
    for i,f in enumerate(frames,1):
        if set(f) != {'ordinal','direction','channel','data'}: raise ValueError('unknown wrapper quarantined')
        if f['ordinal']!=i or f['direction'] not in ('client','server') or f['channel'] not in ('control','protocol'):raise ValueError('frame contract')
        collect(f['data'],ordinal=i)
    def visit(v,field='',parent=''):
        if isinstance(v,dict):
            for k in v:
                if k not in allowed and field not in ('modelUsage','answers'):
                    raise ValueError('unreviewed field quarantined')
            return {k:visit(x,k,field) for k,x in v.items()}
        if isinstance(v,list):return [visit(x,field,parent) for x in v]
        if field=='pid' and isinstance(v,int):return 1000
        if isinstance(v,str):
            if field=='timestamp':
                instant=datetime.datetime.fromisoformat(v.replace('Z','+00:00'))
                if not timebase:timebase.append(instant)
                origin=datetime.datetime(2000,1,1,tzinfo=instant.tzinfo)
                return (origin+(instant-timebase[0])).isoformat(timespec='milliseconds').replace('+00:00','Z')
            if field=='messaging_socket_path' and v:return '/fixture/runtime/socket'
            if (type(v).__name__,v) in ids:return ids[(type(v).__name__,v)]
            v=v.replace(root,'/fixture/project')
            for (typ,old),new in sorted(ids.items(),key=lambda kv:len(str(kv[0][1])),reverse=True):
                if typ=='str' and len(old)>=8:v=v.replace(old,new)
            if SECRET.search(v):raise ValueError('sensitive text quarantined')
            return v
        if isinstance(v,int) and field in IDKEYS:return ids.get((type(v).__name__,v),v)
        return v
    safe=[dict(f,data=visit(f['data'])) for f in frames]
    # Re-collect references and compare occurrence/equality classes after mapping.
    for (typ,old),new in ids.items():
        for ref in refs[(typ,old)]:
            def found(v):
                if isinstance(v,dict):return sum((1 if k==ref['field'] and x==new else 0)+found(x) for k,x in v.items())
                if isinstance(v,list):return sum(found(x) for x in v)
                return 0
            if not found(safe[ref['ordinal']-1]['data']):raise ValueError('ID relationship changed')
    data=''.join(json.dumps(f,separators=(',',':'),ensure_ascii=False)+'\n' for f in safe)
    if SECRET.search(data):raise ValueError('secret scan failed')
    relations=[{'replacement':new,'json_type':'string' if typ=='str' else 'number','namespace':namespaces[(typ,old)],'references':refs[(typ,old)]} for (typ,old),new in ids.items()]
    return data,relations

def selftest():
    fixture={'ordinal':1,'direction':'server','channel':'protocol','data':{'type':'assistant','session_id':'original-session','message':{'id':'original-message','content':[{'type':'tool_use','id':'original-tool','name':'Read','input':{'file_path':'/private/synthetic/numbers.txt'}}]}}}
    fixture['data']['timestamp']='2026-10-18T00:00:00.000Z'
    b=json.dumps(fixture)+'\n';safe,refs=publish(b,'/private/synthetic');assert '2000-01-01T00:00:00.000Z' in safe;assert '/fixture/project/numbers.txt' in safe and 'original-session' not in safe
    for canary in ['sk-ant-abcdefghijklmnop','Authorization: Bearer canary-secret','access_token=x','refresh_token=x','api_key=x','/Users/canary/private','/home/canary/private','canary@example.com']:
        bad=json.loads(b);bad['data']['message']['content']=[{'type':'text','text':canary}]
        try:publish(json.dumps(bad),'elsewhere')
        except ValueError:pass
        else:raise AssertionError('canary accepted')
    bad=json.loads(b);bad['data']['unexpected_private_field']='anything'
    try:publish(json.dumps(bad),'elsewhere')
    except ValueError:pass
    else:raise AssertionError('unknown field accepted')
    numeric={'ordinal':1,'direction':'server','channel':'protocol','data':{'type':'system','session_id':7,'request_id':'7'}}
    nsafe,nrefs=publish(json.dumps(numeric),'elsewhere');assert nrefs[0]['json_type']=='number' and nrefs[1]['json_type']=='string'
    ambiguous={'ordinal':1,'direction':'server','channel':'protocol','data':{'type':'system','session_id':'collision','request_id':'collision'}}
    try:publish(json.dumps(ambiguous),'elsewhere')
    except ValueError:pass
    else:raise AssertionError('ambiguous namespaces accepted')
    print('sanitizer canaries, whitelist, path and typed ID checks passed')
if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--self-test',action='store_true');p.add_argument('--private-dir');p.add_argument('--output');p.add_argument('--cli-version');a=p.parse_args()
    if a.self_test:selftest()
    else:
        if a.cli_version not in VERSION_KEYS: raise ValueError('explicit reviewed CLI version required')
        private=pathlib.Path(a.private_dir)
        status=json.loads((private/'status.json').read_text())
        if status.get('cli_version') != a.cli_version: raise ValueError('private source version mismatch')
        data,relations=publish((private/'raw.jsonl').read_text(),str((private/'project').resolve()),a.cli_version)
        dest=pathlib.Path(a.output);dest.mkdir(parents=True,exist_ok=False)
        (dest/'wire.jsonl').write_text(data);(dest/'id-relations.json').write_text(json.dumps(relations,indent=2)+'\n')
        print('published scanned frames:',len(data.splitlines()),'ID classes:',len(relations),'sha256:',hashlib.sha256(data.encode()).hexdigest())
