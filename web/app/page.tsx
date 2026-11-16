'use client';

import { useEffect, useState, type FormEvent } from 'react';

type Machine = { id: string; name: string; executors: string[]; projects: string[]; seen: string };
type Step = { id: string; name: string; role: string; executor: string; prompt?: string; read_only?: boolean; depends_on: string[] | null; base_from?: string };
type Workflow = { id: string; version: number; name: string; steps: Step[] };
type Artifact = { name: string; sha256: string; bytes: number };
type Run = { id: string; step_id?: string; status: string; spec: { title: string; executor: string }; result?: { detail: string; commit?: string; artifacts?: Artifact[] } };
type Execution = { id: string; workflow_id: string; version: number; project: string; status: string; brief: string; workflow?: Workflow; runs?: Run[]; approved?: string[] };
type RunEvent = { seq: number; kind: string; text: string };

const executors = { approval: '人工确认', codex: 'Codex', 'qoder-cn': 'Qoder CN', godot: 'Godot' };
const label: Record<string, string> = { queued: '排队', claimed: '已领取', running: '运行中', canceling: '取消中', failed: '失败', lost: '失联', canceled: '已取消', succeeded: '已通过', waiting: '等待依赖', ready: '等待确认' };

function newID() { return crypto.randomUUID().replaceAll('-', ''); }

async function api<T>(token: string, path: string, data?: unknown): Promise<T> {
  const response = await fetch('/api/v1' + path, {
    method: data === undefined ? 'GET' : 'POST',
    headers: { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' },
    body: data === undefined ? undefined : JSON.stringify(data),
    cache: 'no-store',
  });
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || '请求失败');
  return result;
}

export default function Console() {
  const [token, setToken] = useState('');
  const [inputToken, setInputToken] = useState('');
  const [notice, setNotice] = useState('');
  const [machines, setMachines] = useState<Machine[]>([]);
  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  const [executions, setExecutions] = useState<Execution[]>([]);
  const [machine, setMachine] = useState('');
  const [project, setProject] = useState('');
  const [machineName, setMachineName] = useState('');
  const [enrollment, setEnrollment] = useState('');
  const [draft, setDraft] = useState<Workflow>();
  const [brief, setBrief] = useState('');
  const [baseCommit, setBaseCommit] = useState('');
  const [executionID, setExecutionID] = useState('');
  const [execution, setExecution] = useState<Execution>();
  const [runID, setRunID] = useState('');
  const [run, setRun] = useState<Run>();
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [busy, setBusy] = useState(false);

  useEffect(() => { setToken(sessionStorage.getItem('agentrib.operator') || ''); }, []);

  useEffect(() => {
    if (!token) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const [m, w, e, detail] = await Promise.all([
          api<Machine[]>(token, '/machines'), api<Workflow[]>(token, '/workflows'), api<Execution[]>(token, '/executions'),
          executionID ? api<Execution>(token, '/executions/' + executionID) : undefined,
        ]);
        if (disposed) return;
        setMachines(m); setWorkflows(w); setExecutions(e); setExecution(detail);
      } catch (error) {
        if (!disposed) setNotice((error as Error).message);
      } finally {
        if (!disposed) timer = setTimeout(refresh, 2500);
      }
    }
    void refresh();
    return () => { disposed = true; clearTimeout(timer); };
  }, [token, executionID]);

  useEffect(() => {
    if (!token || !runID) return;
    let disposed = false;
    let cursor = 0;
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const result = await api<{ run: Run; events: RunEvent[] }>(token, `/runs/${runID}?after=${cursor}`);
        if (disposed) return;
        setRun(result.run);
        if (result.events.length) {
          cursor = result.events[result.events.length - 1].seq;
          setEvents(previous => [...previous, ...result.events]);
        }
      } catch (error) {
        if (!disposed) setNotice((error as Error).message);
      } finally {
        if (!disposed) timer = setTimeout(refresh, 1700);
      }
    }
    void refresh();
    return () => { disposed = true; clearTimeout(timer); };
  }, [token, runID]);

  async function action(fn: () => Promise<void>) {
    if (busy) return;
    setBusy(true); setNotice('');
    try { await fn(); } catch (error) { setNotice((error as Error).message); } finally { setBusy(false); }
  }

  function logIn(event: FormEvent) {
    event.preventDefault();
    const key = inputToken.trim();
    void action(async () => {
      await api<Machine[]>(key, '/machines');
      sessionStorage.setItem('agentrib.operator', key);
      setInputToken(''); setToken(key);
    });
  }

  function logOut() {
    sessionStorage.removeItem('agentrib.operator');
    setToken(''); setEnrollment(''); setDraft(undefined); setMachines([]); setWorkflows([]); setExecutions([]);
    setMachine(''); setProject(''); setBrief(''); setBaseCommit(''); setExecutionID(''); setExecution(undefined); selectRun('');
  }

  function selectRun(id: string) { setRun(undefined); setEvents([]); setRunID(id); }

  function updateStep(index: number, change: Partial<Step>) {
    setDraft(current => current && { ...current, steps: current.steps.map((step, i) => i === index ? { ...step, ...change } : step) });
  }

  const selectedMachine = machines.find(item => item.id === machine);
  const saved = workflows.find(item => item.id === draft?.id && item.version === draft?.version);
  const completed = new Set(execution?.approved || []);
  for (const current of execution?.runs || []) if (current.status === 'succeeded' && current.step_id) completed.add(current.step_id);

  return <>
    <header><div className="frame header"><a className="brand" href="/">Agent<span>rib</span></a><span>Godot 制作控制台</span>{token && <button className="text-button" onClick={logOut}>退出</button>}</div></header>
    <main className="frame">
      {notice && <p className="notice" role="status">{notice}</p>}
      {!token ? <section className="panel login">
        <h1>连接控制面</h1>
        <form onSubmit={logIn}><label>操作员令牌<input type="password" value={inputToken} onChange={event => setInputToken(event.target.value)} autoComplete="off" required /></label><button className="primary" disabled={busy}>连接</button></form>
      </section> : <>
        <h1>制作工作台</h1>
        <section className="panel">
          <h2>执行机器</h2>
          <div className="machines">{machines.length ? machines.map(item => <article key={item.id}><div className="line"><strong>{item.name}</strong><span className="state">{Date.now() - new Date(item.seen).getTime() < 45000 ? '在线' : '离线'}</span></div><p>{item.executors.map(key => executors[key as keyof typeof executors] || key).join(' · ') || '等待 Runner 报告'}</p><p>{item.projects.join(' · ')}</p></article>) : <p>还没有登记 Runner。</p>}</div>
          <form className="inline-form" onSubmit={event => { event.preventDefault(); void action(async () => {
            const result = await api<{ token: string }>(token, '/machines', { name: machineName });
            setEnrollment(result.token); setMachineName('');
          }); }}><label>新机器名称<input value={machineName} onChange={event => setMachineName(event.target.value)} maxLength={80} required /></label><button disabled={busy}>登记机器</button></form>
          {enrollment && <div className="enroll"><p>把此令牌存入执行机器的 Runner 令牌文件。</p><input aria-label="Runner 令牌" readOnly value={enrollment} onFocus={event => event.currentTarget.select()} /><button onClick={() => setEnrollment('')}>收起令牌</button></div>}
        </section>

        <section className="panel">
          <div className="line"><h2>工作流编辑</h2><button disabled={busy} onClick={() => void action(async () => {
            const original = await api<Workflow>(token, '/workflows/template');
            setDraft({ ...original, id: original.id + '-' + newID().slice(0, 6) });
          })}>从 Godot 模板创建</button></div>
          <label>已保存的工作流<select value={saved?.id || ''} onChange={event => setDraft(workflows.find(item => item.id === event.target.value))}><option value="">选择工作流</option>{workflows.map(item => <option key={item.id} value={item.id}>{item.name} · v{item.version}</option>)}</select></label>
          {draft && <>
            <label>工作流名称<input value={draft.name} onChange={event => setDraft({ ...draft, name: event.target.value })} /></label>
            <div className="step-list">{draft.steps.map((step, index) => <fieldset key={index}>
              <legend>{step.id || '新步骤'}</legend>
              <div className="fields"><label>标识<input value={step.id} onChange={event => updateStep(index, { id: event.target.value })} /></label><label>名称<input value={step.name} onChange={event => updateStep(index, { name: event.target.value })} /></label><label>角色<input value={step.role} onChange={event => updateStep(index, { role: event.target.value })} /></label><label>执行方式<select value={step.executor} onChange={event => updateStep(index, { executor: event.target.value, read_only: false })}>{Object.entries(executors).map(([key, name]) => <option key={key} value={key}>{name}</option>)}</select></label></div>
              <div className="fields"><label>前置步骤（逗号分隔）<input value={(step.depends_on || []).join(',')} onChange={event => updateStep(index, { depends_on: event.target.value.split(',') })} /></label><label>提交来源<select value={step.base_from || ''} onChange={event => updateStep(index, { base_from: event.target.value })}><option value="">项目起始提交</option>{(step.depends_on || []).map((id, i) => id.trim() && <option key={i} value={id.trim()}>{id.trim()}</option>)}</select></label></div>
              {(step.executor === 'codex' || step.executor === 'qoder-cn') && <><label>任务说明<textarea value={step.prompt || ''} onChange={event => updateStep(index, { prompt: event.target.value })} /></label><label className="check"><input type="checkbox" checked={step.read_only || false} onChange={event => updateStep(index, { read_only: event.target.checked })} />只读验收</label></>}
              <button className="text-button" disabled={draft.steps.length === 1} onClick={() => setDraft({ ...draft, steps: draft.steps.filter((_, i) => i !== index) })}>移除步骤</button>
            </fieldset>)}</div>
            <div className="actions"><button onClick={() => setDraft({ ...draft, steps: [...draft.steps, { id: 'step-' + newID().slice(0, 6), name: '', role: '', executor: 'codex', depends_on: [] }] })}>添加步骤</button><button className="primary" disabled={busy} onClick={() => void action(async () => {
              const result = await api<Workflow>(token, '/workflows', { ...draft, steps: draft.steps.map(step => ({ ...step, depends_on: (step.depends_on || []).map(id => id.trim()).filter(Boolean) })) });
              setDraft(result); setWorkflows(items => [...items.filter(item => item.id !== result.id), result]); setNotice(`已保存工作流 v${result.version}`);
            })}>保存版本</button></div>
          </>}
        </section>

        <section className="panel">
          <h2>发起制作</h2>
          {saved ? <form onSubmit={event => { event.preventDefault(); void action(async () => {
            const result = await api<Execution>(token, '/executions', { id: newID(), workflow_id: saved.id, version: saved.version, machine_id: machine, project, brief, commit: baseCommit.trim() });
            setExecution(undefined); setExecutionID(result.id); selectRun('');
          }); }}>
            <p>{saved.name} · v{saved.version}</p>
            <div className="fields"><label>执行机器<select value={machine} onChange={event => { setMachine(event.target.value); setProject(''); }} required><option value="">选择机器</option>{machines.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><label>项目<select value={project} onChange={event => setProject(event.target.value)} required><option value="">选择本机项目</option>{selectedMachine?.projects.map(id => <option key={id}>{id}</option>)}</select></label><label>起始提交（选填）<input value={baseCommit} onChange={event => setBaseCommit(event.target.value)} placeholder="默认为本机 HEAD" /></label></div>
            <label>游戏简报<textarea rows={4} value={brief} onChange={event => setBrief(event.target.value)} required /></label>
            <button className="primary" disabled={busy}>启动工作流</button>
          </form> : <p>先选择已保存的工作流。</p>}
        </section>

        <section className="panel">
          <h2>运行记录</h2>
          <label>工作流运行<select value={executionID} onChange={event => { setExecution(undefined); setExecutionID(event.target.value); selectRun(''); }}><option value="">选择运行记录</option>{executions.map(item => <option key={item.id} value={item.id}>{item.project} · {item.workflow_id} v{item.version} · {label[item.status] || item.status} · {item.id.slice(0, 7)}</option>)}</select></label>
          {execution?.workflow && <><details><summary>本次游戏简报</summary><pre>{execution.brief}</pre></details><ol className="timeline">{execution.workflow.steps.map(step => {
            const current = execution.runs?.find(item => item.step_id === step.id);
            const ready = step.executor === 'approval' && execution.status === 'running' && (step.depends_on || []).every(id => completed.has(id)) && !completed.has(step.id);
            const status = current?.status || (completed.has(step.id) ? 'succeeded' : ready ? 'ready' : 'waiting');
            return <li key={step.id}><div><strong>{step.name}</strong><p>{step.role} · {label[status] || status}</p></div><div className="actions">
              {current && <button onClick={() => { if (current.id !== runID) selectRun(current.id); }}>查看日志</button>}
              {ready && <button className="primary" disabled={busy} onClick={() => void action(async () => { await api(token, `/executions/${execution.id}/approve/${step.id}`, {}); })}>确认步骤</button>}
            </div></li>;
          })}</ol></>}
          {run && <article className="run-detail"><div className="line"><h3>{run.spec.title} · {label[run.status] || run.status}</h3>{['queued', 'claimed', 'running'].includes(run.status) && <button disabled={busy} onClick={() => void action(async () => { await api(token, `/runs/${run.id}/cancel`, {}); })}>取消执行</button>}</div>
            {run.result && <p>{run.result.detail}{run.result.commit && <><br />Git 提交 <code>{run.result.commit}</code></>}</p>}
            {run.result?.artifacts?.length ? <ul>{run.result.artifacts.map(item => <li key={item.name}>{item.name} · {Math.ceil(item.bytes / 1024)} KB</li>)}</ul> : null}
            <pre className="log" aria-label="执行日志">{events.length ? events.map(event => `[${event.kind}] ${event.text}`).join('\n') : '等待执行日志…'}</pre>
          </article>}
        </section>
      </>}
    </main>
  </>;
}
