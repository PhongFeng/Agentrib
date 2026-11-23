'use client';

import { useEffect, useId, useState, type FormEvent } from 'react';
import { api, newID } from './api';
import { QAReview, qaRoles, type Bug, type Ticket, type Review } from './qa-review';
import { BriefChat, clearBriefChats } from './brief-chat';
import { isPreview, previewLabel, RunPreviewDialog, type Artifact } from './run-previews';

type AgentQuota = { label?: string; remaining: number; unit: string; window_minutes?: number; resets_at?: number };
type AgentStatus = { account?: string; quotas?: AgentQuota[] };
type Machine = { id: string; name: string; executors: string[]; agent_statuses?: Record<string, AgentStatus>; create_projects?: boolean; brief_chat?: boolean; production_workflow?: boolean; workflow_review?: boolean; can_check_agents?: boolean; seen: string };
type ProjectInput = { id: string; name: string; engine: string };
type Project = ProjectInput & { workspace?: string; status: 'ready' | 'pending' | 'failed' | 'unavailable'; error?: string };
type Engine = { id: string; name: string; implemented: boolean };
type Agent = { id: string; name: string; commands: string[]; read_only: boolean };
type Step = { id: string; name: string; role: string; executor: string; prompt?: string; read_only?: boolean; depends_on: string[] | null; base_from?: string; merge_from?: string[]; qa_gate?: boolean; export_game?: boolean; ci_repair_agent?: string; preflight?: boolean; qa_rework?: boolean };
type Workflow = { id: string; version: number; name: string; engine?: string; steps: Step[] };
type Run = { id: string; step_id?: string; attempt?: number; status: string; created: string; started_at?: string; finished_at?: string; spec: { title: string; executor: string; commit?: string }; result?: { detail: string; commit?: string; artifacts?: Artifact[]; bugs?: Bug[] } };
type Execution = { id: string; workflow_id: string; version: number; project: string; status: string; brief: string; workflow?: Workflow; runs?: Run[]; approved?: string[]; qa_phase?: string; qa_reviews?: Record<string, Review>; qa_tickets?: Record<string, Ticket[]> };
type RunEvent = { seq: number; kind: string; text: string };
type View = 'workbench' | 'history';
type History = { project: string; executions: Execution[]; detail?: Execution };

const executors: Record<string, string> = { approval: '人工确认', engine: '引擎测试与导出', notify: '站内交付通知' };
const label: Record<string, string> = { rework: '返工中', product_review: '待产品介入', environment: '待环境恢复', triaging: '产品分诊中', rechecking: 'QA 复验中', waiting_qa: '待处理', rejected: '本轮验收未通过', queued: '排队', claimed: '已领取', running: '运行中', canceling: '取消中', failed: '失败', lost: '失联', canceled: '已取消', succeeded: '已通过', waiting: '等待依赖', ready: '等待确认' };
const projectStatus: Record<Project['status'], string> = { ready: '工作区就绪', pending: '等待创建工作区', failed: '工作区创建失败', unavailable: '工作区暂不可用' };
const emptyProject: ProjectInput = { id: '', name: '', engine: 'godot' };
const agentAccountLabel: Record<string, string> = { available: '额度可用', limited: '额度受限', signed_in: '已登录', auth_required: '需要认证' };
const quotaUnits: Record<string, string> = { credits: 'Credits', requests: '次请求', tokens: 'Tokens', CNY: '元', USD: '美元' };
const quotaNumber = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 4 });
const runDate = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'medium', hour12: false });
const webBuild = 'godot-web.zip';

function durationText(milliseconds: number) {
  const seconds = Math.max(0, Math.floor(milliseconds / 1000));
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor(seconds / 60) % 60;
  return [hours && `${hours} 小时`, minutes && `${minutes} 分`, `${seconds % 60} 秒`].filter(Boolean).join(' ');
}

function RunTiming({ run, now }: { run?: Run; now: number }) {
  if (!run || ['queued', 'claimed'].includes(run.status) || !run.started_at && run.finished_at) {
    return <p className="run-timing">未开始</p>;
  }
  if (!run.started_at) {
    return <p className="run-timing">{run.created && <span>入队 <time dateTime={run.created}>{runDate.format(new Date(run.created))}</time></span>}
      <span>开始时间和耗时未记录</span></p>;
  }
  const start = new Date(run.started_at);
  const end = run.finished_at ? Date.parse(run.finished_at) : now;
  return <p className="run-timing"><span>开始 <time dateTime={run.started_at}>{runDate.format(start)}</time></span>
    <span>{run.finished_at ? '耗时' : '已运行'} {durationText(end - start.getTime())}</span></p>;
}

function qaBuildFor(step: Step, attempts: Map<string, Run[]>, qa?: Run) {
  const dependencies = new Set([step.base_from || '', ...(step.depends_on || [])]);
  const activeRevision = qa && ['queued', 'claimed', 'running', 'canceling'].includes(qa.status) ? qa.spec.commit : undefined;
  return [...dependencies].flatMap(id => attempts.get(id) || []).find(run =>
    run.status === 'succeeded' && run.result?.commit && (!activeRevision || run.result.commit === activeRevision) &&
    run.result.artifacts?.some(artifact => artifact.name === webBuild));
}

function runStatus(status: string, detail?: string) {
  if (status === 'failed' && detail === 'Execution timed out') return '执行超时';
  return label[status] || status;
}

function agentCheckStatus(run: Run) {
  const detail = run.result?.detail;
  switch (run.status) {
    case 'queued': return '排队等待 Runner';
    case 'claimed':
    case 'running': return '正在测试模型响应…';
    case 'succeeded': return '模型已响应';
    case 'failed': return `测试失败 · ${detail === 'Execution timed out' ? '请求超时' : detail || '模型调用失败'}`;
    case 'lost': return detail === 'Runner lease expired' ? 'Runner 已失联' : detail || '测试未完成';
    default: return runStatus(run.status);
  }
}

const goodTones = new Set(['在线', 'CLI 已就绪', '额度可用', '已登录', '工作区就绪', '已通过', '已通知']);
const warnTones = new Set(['本轮验收未通过', '额度受限', '需要认证', '等待确认', '等待依赖', '排队', '已领取', '等待创建工作区', 'CLI 未就绪']);
const badTones = new Set(['离线', '失败', '失联', '执行超时', '工作区创建失败', '工作区暂不可用']);
const infoTones = new Set(['运行中', '取消中']);
function tone(text: string) {
  if (goodTones.has(text)) return 'good';
  if (warnTones.has(text)) return 'warn';
  if (badTones.has(text)) return 'bad';
  if (infoTones.has(text)) return 'info';
  return 'neutral';
}

function BrandMark({ height = 20 }: { height?: number }) {
  const id = useId().replace(/:/g, '');
  return <svg className="brand-mark" width={Math.round(height * 220 / 96)} height={height} viewBox="20 82 220 96" fill="none" aria-hidden="true">
    <defs><linearGradient id={id} x1="45" y1="130" x2="220" y2="130" gradientUnits="userSpaceOnUse">
      <stop stopColor="#52D9C4" /><stop offset=".52" stopColor="#4BB9E8" /><stop offset="1" stopColor="#7189F5" />
    </linearGradient></defs>
    <g stroke={`url(#${id})`} strokeWidth="13" strokeLinecap="round" strokeLinejoin="round">
      <path d="M30 92h52c26 0 36 10 52 29l7 9c16 19 26 29 52 29h38" />
      <path d="M30 168h52c26 0 36-10 52-29l7-9c16-19 26-29 52-29h38" />
    </g>
  </svg>;
}

function quotaText(quota: AgentQuota) {
  const minutes = quota.window_minutes || 0;
  let window = '';
  if (minutes > 0) {
    if (minutes % 1440 === 0) window = `${minutes / 1440} 天`;
    else if (minutes % 60 === 0) window = `${minutes / 60} 小时`;
    else window = `${minutes} 分钟`;
  }
  const value = quotaNumber.format(quota.remaining);
  const amount = quota.unit === 'percent' ? value + '%' : value + ' ' + (quotaUnits[quota.unit] || quota.unit);
  return [quota.label, window, '剩余 ' + amount].filter(Boolean).join(' · ');
}

export default function Console() {
  const [token, setToken] = useState('');
  const [inputToken, setInputToken] = useState('');
  const [notice, setNotice] = useState('');
  const [machines, setMachines] = useState<Machine[]>();
  const [engines, setEngines] = useState<Engine[]>([]);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [agentChecks, setAgentChecks] = useState<Run[]>([]);
  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [project, setProject] = useState('');
  const [projectDraft, setProjectDraft] = useState<ProjectInput>();
  const [draft, setDraft] = useState<Workflow>();
  const [brief, setBrief] = useState('');
  const [view, setView] = useState<View>('workbench');
  const [historyProject, setHistoryProject] = useState('');
  const [history, setHistory] = useState<History>();
  const [executionID, setExecutionID] = useState('');
  const [runID, setRunID] = useState('');
  const [previewRunID, setPreviewRunID] = useState('');
  const [runData, setRun] = useState<Run>();
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [busy, setBusy] = useState(false);

  useEffect(() => { setToken(sessionStorage.getItem('agentrib.operator') || ''); }, []);

  useEffect(() => {
    if (!token) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const [inventory, catalog, agentCatalog, w] = await Promise.all([
          api<Machine[]>(token, '/machines').then(async machines => {
            const single = machines.length === 1 ? machines[0] : undefined;
            const [projects, checks] = await Promise.all([
              single ? api<Project[]>(token, `/machines/${single.id}/projects`) : [],
              single?.can_check_agents ? api<Run[]>(token, `/machines/${single.id}/agent-checks`) : [],
            ]);
            return { machines, projects, checks };
          }),
          api<Engine[]>(token, '/engines'), api<Agent[]>(token, '/agents'), api<Workflow[]>(token, '/workflows'),
        ]);
        if (disposed) return;
        setMachines(inventory.machines); setProjects(inventory.projects); setAgentChecks(inventory.checks);
        setEngines(catalog); setAgents(agentCatalog); setWorkflows(w);
      } catch (error) {
        if (!disposed) setNotice((error as Error).message);
      } finally {
        if (!disposed) timer = setTimeout(refresh, 2500);
      }
    }
    void refresh();
    return () => { disposed = true; clearTimeout(timer); };
  }, [token]);

  useEffect(() => {
    if (!token || view !== 'history' || !historyProject) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const list = await api<Execution[]>(token, '/executions?project=' + encodeURIComponent(historyProject));
        if (disposed) return;
        const executions = list.filter(item => item.project === historyProject);
        const selected = executions.some(item => item.id === executionID) ? executionID : executions[0]?.id || '';
        if (selected !== executionID) {
          setExecutionID(selected);
          selectRun('');
        }
        const detail = selected ? await api<Execution>(token, '/executions/' + selected) : undefined;
        if (!disposed) setHistory({ project: historyProject, executions, detail });
      } catch (error) {
        if (!disposed) setNotice((error as Error).message);
      } finally {
        if (!disposed) timer = setTimeout(refresh, 2500);
      }
    }
    void refresh();
    return () => { disposed = true; clearTimeout(timer); };
  }, [token, view, historyProject, executionID]);

  const machine = machines?.length === 1 ? machines[0] : undefined;
  const machineOnline = !!machine && Date.now() - new Date(machine.seen).getTime() < 45000;
  const machineAgents = agents.filter(agent => machine?.agent_statuses?.[agent.id] || machine?.executors.includes(agent.id));
  const machineEngines = engines.filter(engine => machine?.executors.includes(engine.id));
  const machineID = machine?.id;
  const selectedProject = projects.find(item => item.id === project);
  const projectReady = selectedProject?.status === 'ready';
  const executions = history?.project === historyProject ? history?.executions : undefined;
  const execution = history?.detail?.id === executionID && history.detail.project === historyProject ? history.detail : undefined;
  const activeRunID = view === 'history' && execution?.runs?.some(item => item.id === runID) ? runID : '';
  const run = activeRunID && runData?.id === activeRunID ? runData : undefined;
  const previewRun = view === 'history' && previewRunID ? execution?.runs?.find(item => item.id === previewRunID) : undefined;

  useEffect(() => {
    if (!token || !activeRunID) return;
    let disposed = false;
    let cursor = 0;
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const result = await api<{ run: Run; events: RunEvent[] }>(token, `/runs/${activeRunID}?summary=1&after=${cursor}`);
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
  }, [token, activeRunID]);

  useEffect(() => {
    if (run?.id) document.getElementById('run-result')?.scrollIntoView({ block: 'start', behavior: 'instant' });
  }, [run?.id]);

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
    clearBriefChats();
    setToken(''); setDraft(undefined); setMachines(undefined); setEngines([]); setAgents([]); setWorkflows([]); setAgentChecks([]);
    setProjects([]); setProjectDraft(undefined); setProject('');
    setBrief(''); setView('workbench'); selectHistoryProject('');
  }

  async function requestProject(input: ProjectInput) {
    await api(token, `/machines/${machineID}/projects`, { ...input, name: input.name.trim() });
    setProjectDraft(undefined);
    setProject(input.id);
  }

  function selectRun(id: string) {
    if (id && id === runID) {
      document.getElementById('run-result')?.scrollIntoView({ block: 'start', behavior: 'instant' });
      return;
    }
    setRun(undefined); setEvents([]); setRunID(id); setPreviewRunID('');
  }

  function runActions(item: Run) {
    const preview = previewLabel(item.result?.artifacts);
    return <>
      {preview && <button className="preview-action" aria-haspopup="dialog" onClick={() => setPreviewRunID(item.id)}>{preview}</button>}
      <button onClick={() => selectRun(item.id)}>查看日志</button>
    </>;
  }

  function selectHistoryProject(id: string, execution = '') {
    setHistoryProject(id); setHistory(undefined); setExecutionID(execution); selectRun('');
  }

  function selectView(next: View) {
    if (next === 'workbench') selectRun('');
    setView(next);
  }

  async function downloadArtifact(id: string, name: string) {
    const path = `/api/v1/runs/${id}/artifacts/${encodeURIComponent(name)}`;
    const response = await fetch(path, {
      method: 'POST',
      headers: { Authorization: 'Bearer ' + token },
      signal: AbortSignal.timeout(15000),
    });
    if (response.status === 401) throw new Error('下载授权已失效，请重新连接控制面。');
    if (response.status === 404) throw new Error(`服务器上找不到 ${name}。`);
    if (!response.ok) throw new Error(`无法下载 ${name}`);
    const link = document.createElement('a');
    link.href = path;
    link.download = name;
    document.body.append(link);
    link.click();
    link.remove();
    setNotice('已交给浏览器下载，可在浏览器的下载列表里查看进度。');
  }

  async function playBuild(id: string) {
    const tab = window.open('about:blank', '_blank');
    if (!tab) throw new Error('浏览器阻止了新标签页，请允许工作台打开新标签页。');
    tab.opener = null;
    try {
      const { url } = await api<{ url: string }>(token, `/runs/${id}/play`, {});
      tab.location.replace(url);
    } catch (error) {
      tab.close();
      throw error;
    }
  }

  function updateStep(index: number, change: Partial<Step>) {
    setDraft(current => current && { ...current, steps: current.steps.map((step, i) => i === index ? { ...step, ...change } : step) });
  }

  const saved = workflows.find(item => item.id === draft?.id && item.version === draft?.version);
  const savedEngine = engines.find(engine => engine.id === (saved?.engine || 'godot'));
  const engineChanged = saved && (saved.engine || 'godot') !== (draft?.engine || 'godot');
  const agentsByID = new Map(agents.map(agent => [agent.id, agent]));
  const missingAgents = machine ? agents.filter(agent => saved?.steps.some(step => step.executor === agent.id || step.ci_repair_agent === agent.id) &&
    !machine.executors.includes(agent.id)) : [];
  const projectMatches = selectedProject?.engine === savedEngine?.id;
  const requiresRunnerUpdate = (!machine?.production_workflow && saved?.steps.some(step => step.merge_from?.length || step.qa_gate || step.export_game)) ||
    (!machine?.workflow_review && saved?.steps.some(step => step.preflight || step.qa_rework));
  const availableForRun = !requiresRunnerUpdate && !engineChanged && !missingAgents.length && savedEngine?.implemented && projectReady && projectMatches &&
    (!saved?.steps.some(step => step.executor === 'engine' || step.executor === savedEngine.id) || machine?.executors.includes(savedEngine.id));
  const engineName = (id = 'godot') => engines.find(engine => engine.id === id)?.name || id;
  const executorName = (id: string) => executors[id] || agentsByID.get(id)?.name || engineName(id);
  const executionEngine = engineName(execution?.workflow?.engine);
  const attemptsByStep = new Map<string, Run[]>();
  for (const current of execution?.runs || []) {
    if (!current.step_id) continue;
    const attempts = attemptsByStep.get(current.step_id) || [];
    attempts.push(current);
    attemptsByStep.set(current.step_id, attempts);
  }
  for (const attempts of attemptsByStep.values()) attempts.sort((a, b) => (b.attempt ?? 1) - (a.attempt ?? 1));
  const currentRuns = Array.from(attemptsByStep.values(), attempts => attempts[0]);
  const completed = new Set(execution?.approved || []);
  for (const current of currentRuns) if (current.status === 'succeeded' && current.step_id) completed.add(current.step_id);
  const canContinue = execution?.status === 'failed' && currentRuns.some(current => ['rejected', 'failed', 'lost', 'canceled'].includes(current.status));
  const resumeQA = canContinue && Object.values(execution?.qa_reviews || {}).some(review => review.phase !== 'rechecking');
  const deliveryFinished = execution?.status === 'succeeded' && execution.workflow?.steps.some(step => step.executor === 'notify' && completed.has(step.id));
  const exportRun = currentRuns.find(item => item.status === 'succeeded' && item.result?.artifacts?.some(artifact => artifact.name === webBuild));
  const qaRuns = (execution?.workflow?.steps || []).filter(step => step.qa_gate).map(step => attemptsByStep.get(step.id)?.[0]);
  const qaRun = qaRuns.find(item => item?.status === 'succeeded');
  const delivered = deliveryFinished && qaRuns.length > 0 && qaRuns.every(item => item?.status === 'succeeded');
  const deferredTickets = Object.values(execution?.qa_tickets || {}).flat().filter(ticket => ticket.deferred_reason).length;
  const now = Date.now();

  return <>
    <header><div className="frame header"><a className="brand" href="/"><BrandMark height={20} /><span className="brand-name">Agent<span>rib</span></span></a><span className="header-sub">游戏制作控制台</span>{token && <button className="text-button" onClick={logOut}>退出</button>}</div></header>
    <main className="frame">
      {notice && <p className="notice" role="status">{notice}</p>}
      {!token ? <section className="panel login">
        <BrandMark height={34} />
        <h1>连接控制面</h1>
        <p className="login-sub">输入操作员令牌，接入工作室的研发流水线。</p>
        <form onSubmit={logIn}><label>操作员令牌<input type="password" value={inputToken} onChange={event => setInputToken(event.target.value)} autoComplete="off" required /></label><button className="primary" disabled={busy}>连接</button></form>
      </section> : <>
        <div className="console-tabs" role="tablist" aria-label="工作台视图" onKeyDown={event => {
          const next = event.key === 'Home' ? 'workbench' : event.key === 'End' ? 'history' :
            ['ArrowLeft', 'ArrowRight'].includes(event.key) ? (view === 'workbench' ? 'history' : 'workbench') : undefined;
          if (!next) return;
          event.preventDefault();
          selectView(next);
          event.currentTarget.querySelector<HTMLButtonElement>(`#tab-${next}`)?.focus();
        }}>
          <button type="button" id="tab-workbench" role="tab" aria-selected={view === 'workbench'} aria-controls="panel-workbench" tabIndex={view === 'workbench' ? 0 : -1} onClick={() => selectView('workbench')}>制作工作台</button>
          <button type="button" id="tab-history" role="tab" aria-selected={view === 'history'} aria-controls="panel-history" tabIndex={view === 'history' ? 0 : -1} onClick={() => selectView('history')}>运行记录</button>
        </div>
        <div id="panel-workbench" role="tabpanel" aria-labelledby="tab-workbench" hidden={view !== 'workbench'}>
        <section className="panel">
          <h2>执行机器</h2>
          {machine ? <article className="machine">
            <div className="line">
              <strong>{machine.name}</strong>
              <span className={'state ' + (machineOnline ? 'good' : 'bad')}>{machineOnline ? '在线' : '离线'}</span>
            </div>
            {!machineOnline && (machineAgents.length > 0 || machineEngines.length > 0) && <p>以下为 Runner 上次上报的信息。</p>}
            <div className="machine-capabilities">
              <section aria-labelledby="machine-agents">
                <h3 id="machine-agents">已安装的 Agent</h3>
                {machineAgents.length === 0 ? <p>暂无安装记录</p> : <ul className="machine-inventory">{machineAgents.map(agent => {
                  const status = machine.agent_statuses?.[agent.id];
                  const ready = machine.executors.includes(agent.id);
                  const account = status?.account ? agentAccountLabel[status.account] : '';
                  const availability = !ready ? 'CLI 未就绪' : account || 'CLI 已就绪';
                  const check = agentChecks.find(run => run.spec.executor === agent.id);
                  const checking = check && ['queued', 'claimed', 'running', 'canceling'].includes(check.status);
                  return <li key={agent.id}>
                    <div className="line"><strong>{agent.name}</strong><span className={'state ' + tone(availability)}>{availability}</span></div>
                    {status?.quotas?.length ? <ul className="agent-quotas">{status.quotas.map((quota, index) => <li key={index}>
                      {quotaText(quota)}{quota.resets_at ? ' · ' + new Date(quota.resets_at * 1000).toLocaleString('zh-CN', { dateStyle: 'short', timeStyle: 'short' }) + ' 重置' : ''}
                    </li>)}</ul> : <>
                      <div className="line agent-check"><p>额度未知</p><button
                        disabled={busy || checking || !ready || !machineOnline || !machine.can_check_agents}
                        title={machine.can_check_agents ? '发送一次短模型请求，可能消耗少量额度' : '更新 Runner 后可测试'}
                        onClick={() => void action(async () => {
                          const started = await api<Run>(token, `/machines/${machine.id}/agent-checks`, { executor: agent.id });
                          setAgentChecks(current => [...current.filter(run => run.spec.executor !== agent.id), started]);
                        })}>测试可用性</button></div>
                      {check && <p role="status">{agentCheckStatus(check)}{check.finished_at && <> · <time dateTime={check.finished_at}>{runDate.format(new Date(check.finished_at))}</time></>}</p>}
                    </>}
                  </li>;
                })}</ul>}
              </section>
              <section aria-labelledby="machine-engines">
                <h3 id="machine-engines">游戏引擎</h3>
                {machineEngines.length ? <ul className="machine-inventory">{machineEngines.map(engine => <li key={engine.id}>
                  <div className="line"><strong>{engine.name}</strong><span className="state good">CLI 已就绪</span></div>
                </li>)}</ul> : <p>暂无引擎记录</p>}
              </section>
            </div>
          </article> : <p>{!machines ? '正在读取执行机器…' : machines.length === 0
            ? '尚未配置执行机器。请在部署时接入 Runner。'
            : '检测到多台执行机器。请在部署端配置一台机器后再启动工作流。'}</p>}
        </section>

        <section className="panel" aria-labelledby="game-projects-heading">
          <div className="line"><h2 id="game-projects-heading">游戏项目</h2>
            {machine?.create_projects && <button disabled={busy} aria-expanded={Boolean(projectDraft)} onClick={() => setProjectDraft(projectDraft ? undefined : emptyProject)}>{projectDraft ? '取消创建' : '创建项目'}</button>}
          </div>
          {machine && !machine.create_projects && <p>更新执行机器上的 Runner 后可创建项目。</p>}
          {projects.length > 0 && <label>选择项目<select value={selectedProject?.id || ''} onChange={event => setProject(event.target.value)}>
            <option value="">选择游戏项目</option>
            {projects.map(item => <option key={item.id} value={item.id}>{item.name} · {engineName(item.engine)}{item.status === 'ready' ? '' : ' · ' + projectStatus[item.status]}</option>)}
          </select></label>}
          {machine && projects.length === 0 && <p>暂无游戏项目。</p>}
          {!machine && <p>接入执行机器后可查看和创建项目。</p>}
          {selectedProject && <div className="project-details">
            <div className="line"><strong>{selectedProject.name === selectedProject.id ? engineName(selectedProject.engine) : selectedProject.id + ' · ' + engineName(selectedProject.engine)}</strong><span className={'state ' + tone(projectStatus[selectedProject.status])} role="status">{projectStatus[selectedProject.status]}</span></div>
            {selectedProject.workspace && <p>Git 工作区 <code>{selectedProject.workspace}</code></p>}
            {selectedProject.error && <p>{selectedProject.error}</p>}
            {selectedProject.status === 'failed' && machine?.create_projects && <button disabled={busy} onClick={() => void action(() => requestProject({
              id: selectedProject.id, name: selectedProject.name, engine: selectedProject.engine,
            }))}>重试创建</button>}
          </div>}
          {projectDraft && machine?.create_projects && <form className="project-details" onSubmit={event => {
            event.preventDefault();
            void action(() => requestProject(projectDraft));
          }}>
            <div className="fields">
              <label>项目名称<input value={projectDraft.name} maxLength={40} required onChange={event => setProjectDraft({ ...projectDraft, name: event.target.value })} /></label>
              <label>项目标识<input value={projectDraft.id} pattern="[a-z0-9][a-z0-9\-]{0,47}" placeholder="例如 moon-power" maxLength={48} required onChange={event => setProjectDraft({ ...projectDraft, id: event.target.value })} /></label>
              <label>项目引擎<select value={projectDraft.engine} onChange={event => setProjectDraft({ ...projectDraft, engine: event.target.value })}>
                {engines.filter(engine => engine.implemented).map(engine => <option key={engine.id} value={engine.id}>{engine.name}</option>)}
              </select></label>
            </div>
            <p>Runner 会以项目标识创建 Git 工作区并写入初始提交。</p>
            <button className="primary" disabled={busy || !projectDraft.name.trim()}>创建 Git 工作区</button>
          </form>}
        </section>

        <section className="panel">
          <div className="line"><h2>工作流编辑</h2><button disabled={busy} onClick={() => void action(async () => {
            const original = await api<Workflow>(token, '/workflows/template');
            setDraft({ ...original, engine: selectedProject?.engine || original.engine, id: original.id + '-' + newID().slice(0, 6) });
          })}>从游戏模板创建</button></div>
          <label>已保存的工作流<select value={saved?.id || ''} onChange={event => setDraft(workflows.find(item => item.id === event.target.value))}><option value="">{workflows.length ? '选择工作流' : '暂无已保存的工作流'}</option>{workflows.map(item => <option key={item.id} value={item.id}>{item.name} · v{item.version}</option>)}</select></label>
          {draft && <>
            <div className="fields"><label>工作流名称<input value={draft.name} onChange={event => setDraft({ ...draft, name: event.target.value })} /></label>
              <label>游戏引擎<select value={draft.engine || 'godot'} onChange={event => {
                const previous = draft.engine || 'godot';
                setDraft({ ...draft, engine: event.target.value, steps: draft.steps.map(step => step.executor === previous ? { ...step, executor: 'engine' } : step) });
              }}>{engines.map(engine => <option key={engine.id} value={engine.id}>{engine.name}{engine.implemented ? '' : ' · 计划接入'}</option>)}</select></label></div>
            {engines.some(engine => engine.id === draft.engine && !engine.implemented) && <p>该引擎可保存到工作流，接入 Runner 适配器后可运行。</p>}
            <div className="step-list">{draft.steps.map((step, index) => {
              const engineStep = step.executor === 'engine' || step.executor === (draft.engine || 'godot');
              const automatic = step.executor === 'approval' || step.executor === 'notify';
              const sources = (step.depends_on || []).map(id => id.trim()).filter(id =>
                draft.steps.some(source => source.id === id && source.executor !== 'approval' && source.executor !== 'notify'));
              return <fieldset key={index}>
              <legend>{step.id || '新步骤'}</legend>
              <div className="fields"><label>标识<input value={step.id} onChange={event => updateStep(index, { id: event.target.value })} /></label><label>名称<input value={step.name} onChange={event => updateStep(index, { name: event.target.value })} /></label><label>角色<input value={step.role} onChange={event => updateStep(index, { role: event.target.value })} /></label><label>执行方式<select value={step.executor} onChange={event => {
                const executor = event.target.value;
                const readOnly = Boolean(step.read_only && agentsByID.get(executor)?.read_only);
                const isEngine = executor === 'engine' || executor === (draft.engine || 'godot');
                const qaRework = Boolean(step.qa_rework && agentsByID.has(executor));
                updateStep(index, {
                  executor, read_only: readOnly, qa_gate: Boolean(step.qa_gate && (readOnly || qaRework)), qa_rework: qaRework,
                  preflight: Boolean(step.preflight && !readOnly && agentsByID.has(executor)),
                  export_game: Boolean(step.export_game && isEngine), ci_repair_agent: isEngine ? step.ci_repair_agent : '',
                  ...(executor === 'approval' || executor === 'notify' ? { base_from: '', merge_from: [] } : {}),
                });
              }}>
                <option value="">选择 Agent 或执行方式</option>
                <optgroup label="Agent">{agents.map(agent => <option key={agent.id} value={agent.id}>{agent.name}</option>)}</optgroup>
                {Object.entries(executors).map(([key, name]) => <option key={key} value={key}>{name}</option>)}
                {step.executor && !executors[step.executor] && !agentsByID.has(step.executor) && <option value={step.executor}>{engineName(step.executor)}</option>}
              </select></label></div>
              <div className="fields">
                <label>前置步骤（逗号分隔）<input value={(step.depends_on || []).join(',')} onChange={event => updateStep(index, { depends_on: event.target.value.split(',') })} /></label>
                {!automatic && <label>主要提交来源<select value={step.base_from || ''} onChange={event => {
                  const base = event.target.value;
                  updateStep(index, { base_from: base, merge_from: base ? (step.merge_from || []).filter(id => id !== base) : [] });
                }}><option value="">项目起始提交</option>{sources.map(id => <option key={id} value={id}>{id}</option>)}</select></label>}
              </div>
              {!automatic && step.base_from && sources.some(id => id !== step.base_from) && <fieldset className="merge-sources">
                <legend>同时合并这些步骤的产出</legend>
                {sources.filter(id => id !== step.base_from).map(id => <label className="check" key={id}>
                  <input type="checkbox" checked={step.merge_from?.includes(id) || false} onChange={event => updateStep(index, {
                    merge_from: event.target.checked ? [...(step.merge_from || []), id] : (step.merge_from || []).filter(source => source !== id),
                  })} />{id}</label>)}
              </fieldset>}
              {(!step.executor || agentsByID.has(step.executor)) && <label>任务说明<textarea value={step.prompt || ''} onChange={event => updateStep(index, { prompt: event.target.value })} /></label>}
              {(!step.executor || agentsByID.get(step.executor)?.read_only) && <label className="check"><input type="checkbox" checked={step.read_only || false} onChange={event => updateStep(index, { read_only: event.target.checked, qa_gate: Boolean((event.target.checked || step.qa_rework) && step.qa_gate), preflight: event.target.checked ? false : step.preflight })} />只读验收</label>}
              {(step.read_only || step.qa_rework) && <label className="check"><input type="checkbox" checked={step.qa_gate || false} disabled={step.qa_rework} onChange={event => updateStep(index, { qa_gate: event.target.checked, preflight: event.target.checked ? false : step.preflight })} />验收通过后才允许交付</label>}
              {(!step.executor || agentsByID.has(step.executor)) && !step.read_only && !step.qa_gate && <label className="check"><input type="checkbox" checked={step.preflight || false} onChange={event => updateStep(index, { preflight: event.target.checked })} />提交给 CI 前检查编译与启动</label>}
              {(!step.executor || agentsByID.has(step.executor)) && <label className="check"><input type="checkbox" checked={step.qa_rework || false} onChange={event => updateStep(index, { qa_rework: event.target.checked, qa_gate: event.target.checked || Boolean(step.read_only && step.qa_gate), preflight: event.target.checked ? false : step.preflight })} />验收失败后派发缺陷单并复验</label>}
              {engineStep && <label className="check"><input type="checkbox" checked={step.export_game || false} onChange={event => updateStep(index, { export_game: event.target.checked })} />必须生成试玩包</label>}
              {engineStep && <label>CI 失败处理 Agent<select value={step.ci_repair_agent || ''} onChange={event => updateStep(index, { ci_repair_agent: event.target.value })}>
                <option value="">未指定（代码问题交给开发 Agent）</option>
                {agents.map(agent => <option value={agent.id} key={agent.id}>{agent.name}</option>)}
              </select></label>}
              <button className="text-button" disabled={draft.steps.length === 1} onClick={() => setDraft({ ...draft, steps: draft.steps.filter((_, i) => i !== index) })}>移除步骤</button>
            </fieldset>})}</div>
            <div className="actions"><button onClick={() => setDraft({ ...draft, steps: [...draft.steps, { id: 'step-' + newID().slice(0, 6), name: '', role: '', executor: '', depends_on: [] }] })}>添加步骤</button><button className="primary" disabled={busy || draft.steps.some(step => !step.executor)} onClick={() => void action(async () => {
              const result = await api<Workflow>(token, '/workflows', { ...draft, steps: draft.steps.map(step => ({ ...step, depends_on: (step.depends_on || []).map(id => id.trim()).filter(Boolean) })) });
              setDraft(result); setWorkflows(items => [...items.filter(item => item.id !== result.id), result]); setNotice(`已保存工作流 v${result.version}`);
            })}>保存版本</button></div>
          </>}
        </section>

        <section className="panel">
          <h2>发起制作</h2>
          {machine?.brief_chat && selectedProject && projectReady && <BriefChat
            key={machine.id + '/' + selectedProject.id}
            token={token} machine={machine.id} project={selectedProject.id} online={machineOnline}
            agents={agents.filter(agent => agent.read_only && machine.executors.includes(agent.id))}
            onApply={setBrief}
          />}
          {saved ? <form onSubmit={event => {
            event.preventDefault();
            if (!machine || !availableForRun) return;
            void action(async () => {
              const result = await api<Execution>(token, '/executions', { id: newID(), workflow_id: saved.id, version: saved.version, machine_id: machine.id, project, brief });
              selectHistoryProject(result.project, result.id);
              selectView('history');
              document.getElementById('tab-history')?.focus();
            });
          }}>
            <p>{saved.name} · v{saved.version} · {savedEngine?.name}{selectedProject ? ' · ' + selectedProject.name : ''}</p>
            {!selectedProject && <p>在「游戏项目」中选择本次制作使用的 Git 工作区。</p>}
            {selectedProject && !projectReady && <p>Git 工作区就绪后可启动制作。</p>}
            {selectedProject && !projectMatches && <p>此项目使用 {engineName(selectedProject.engine)}。请选择使用相同引擎的工作流。</p>}
            {engineChanged && <p>保存引擎变更后可启动工作流。</p>}
            {missingAgents.length > 0 && <p>此 Runner 尚未发现 {missingAgents.map(agent => agent.name).join('、')}。</p>}
            {requiresRunnerUpdate && <p>此工作流需要新版 Runner。</p>}
            {savedEngine && !savedEngine.implemented && <p>{savedEngine.name} 适配器计划接入，当前可编辑和保存此流程。</p>}
            {projectReady && projectMatches && <p>执行时从项目当前已提交的 Git 版本创建独立工作区。</p>}
            <label>游戏简报<textarea rows={4} value={brief} onChange={event => setBrief(event.target.value)} required /></label>
            <button className="primary" disabled={busy || !availableForRun}>启动工作流</button>
          </form> : <p>先选择已保存的工作流。</p>}
        </section>
        </div>

        <section id="panel-history" role="tabpanel" aria-labelledby="tab-history" className="panel" hidden={view !== 'history'}>
          <h2>运行记录</h2>
          <label>游戏项目<select value={historyProject} onChange={event => selectHistoryProject(event.target.value)}>
            <option value="">选择游戏项目</option>
            {projects.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
          </select></label>
          {!historyProject ? <p>选择游戏项目后查看该项目的运行状态和日志。</p> :
            !executions ? <p role="status">正在加载该项目的运行记录…</p> :
            executions.length === 0 ? <p role="status">该项目暂无运行记录。</p> :
              <label>工作流运行<select value={executionID} onChange={event => { setExecutionID(event.target.value); selectRun(''); }}>{executions.map(item => <option key={item.id} value={item.id}>{item.workflow_id} v{item.version} · {label[item.qa_phase || item.status] || item.status} · {item.id.slice(0, 7)}</option>)}</select></label>}
          {canContinue && execution && <div className="notice line">
            <p>{resumeQA ? '已有当前构建的 QA 缺陷单，可以继续返工。' : '问题修复后可以继续。已通过的步骤和产出会保留。'}</p>
            <button className="primary" disabled={busy} onClick={() => void action(async () => {
              const resumed = await api<Execution>(token, `/executions/${execution.id}/continue`, {});
              setHistory(current => current?.detail?.id === resumed.id && current.project === resumed.project ? {
                ...current,
                detail: resumed,
                executions: current.executions.map(item => item.id === resumed.id ? { ...item, status: resumed.status } : item),
              } : current);
            })}>{resumeQA ? '恢复 QA 返工' : '从失败处继续'}</button>
          </div>}
          {deliveryFinished && <div className="notice">
            <p role="status">{delivered
              ? deferredTickets > 0 ? `本轮验收范围已通过，${deferredTickets} 张缺陷单延期验证。` : 'QA 已通过，可以交付。'
              : '工作流已结束，未记录 QA 通过。'}</p>
            {(qaRun || exportRun) && <div className="actions">
              {qaRun && <button onClick={() => { if (qaRun.id !== runID) selectRun(qaRun.id); }}>查看 QA 报告</button>}
              {exportRun && <button disabled={busy} onClick={() => void action(() => downloadArtifact(exportRun.id, webBuild))}>下载试玩包</button>}
            </div>}
          </div>}
          {execution?.workflow && <><details><summary>本次游戏简报</summary><pre>{execution.brief}</pre></details><ol className="timeline">{execution.workflow.steps.map(step => {
            const [current, ...history] = attemptsByStep.get(step.id) || [];
            const qaBuild = step.qa_gate && !deliveryFinished ? qaBuildFor(step, attemptsByStep, current) : undefined;
            const review = ['running', 'waiting_qa'].includes(execution.status) ? execution.qa_reviews?.[step.id] : undefined;
            const reviewed = review ? [current, ...history].find(attempt => attempt?.id === review.run_id) : current;
            const repairs = [':triage', ':repair', ':fix:art', ':fix:audio', ':fix:develop'].flatMap(suffix => attemptsByStep.get(step.id + suffix) || []);
            const ready = step.executor === 'approval' && execution.status === 'running' && (step.depends_on || []).every(id => completed.has(id)) && !completed.has(step.id);
            const status = current?.status || (completed.has(step.id) ? 'succeeded' : ready ? 'ready' : 'waiting');
            return <li key={step.id} data-tone={tone(runStatus(status, current?.result?.detail))}><div><strong>{step.name}</strong><p>{step.role} · {step.executor === 'engine' ? executionEngine : executorName(step.executor)} · {step.executor === 'notify' && completed.has(step.id) ? '已通知' : runStatus(status, current?.result?.detail)}{(current?.attempt ?? 1) > 1 && ` · 第 ${current.attempt} 次`}</p>
              {step.executor !== 'approval' && step.executor !== 'notify' && <RunTiming run={current} now={now} />}
              {qaBuild && <div className="qa-preview">
                <button disabled={busy} onClick={() => void action(() => downloadArtifact(qaBuild.id, webBuild))}>下载 QA 试玩包</button>
                <button className="preview-action" disabled={busy} onClick={() => void action(() => playBuild(qaBuild.id))}>打开游戏试玩</button>
                <span>CI 构建 <code title={qaBuild.result?.commit}>{qaBuild.result?.commit?.slice(0, 7)}</code> · 测试版</span>
              </div>}
              {history.length > 0 && <details><summary>历史尝试</summary><div className="run-attempts">{history.map(previous => <div key={previous.id}>
                <p>第 {previous.attempt ?? 1} 次 · {runStatus(previous.status, previous.result?.detail)}</p>
                <RunTiming run={previous} now={now} />
                <div className="actions">{runActions(previous)}</div>
              </div>)}</div></details>}
              {Boolean(current?.result?.bugs?.length) && <p>{current.result?.bugs?.length} 张 QA 缺陷单</p>}
              {execution.qa_tickets?.[step.id]?.length ? <QAReview key={reviewed?.id} tickets={execution.qa_tickets[step.id]}
                review={review} bugs={reviewed?.result?.bugs || []} busy={busy} decide={body => void action(async () => {
                  if (!review) return;
                  await api(token, `/executions/${execution.id}/qa/${review.run_id}`, body);
                  const detail = await api<Execution>(token, '/executions/' + execution.id);
                  setHistory(h => h?.detail?.id === detail.id ? { ...h, detail, executions: h.executions.map(item => item.id === detail.id ? detail : item) } : h);
                })} /> : null}
              {repairs.length > 0 && <details><summary>{step.qa_rework ? '缺陷返工记录' : 'CI 失败处理记录'}</summary><div className="run-attempts">{repairs.map(repair => <div key={repair.id}>
                <p>{repair.spec.title} · {executorName(repair.spec.executor)} · {runStatus(repair.status, repair.result?.detail)} · 第 {repair.attempt ?? 1} 次</p>
                <RunTiming run={repair} now={now} />
                <div className="actions">{runActions(repair)}</div>
              </div>)}</div></details>}
            </div><div className="actions">
              {current && runActions(current)}
              {ready && <button className="primary" disabled={busy} onClick={() => void action(async () => { await api(token, `/executions/${execution.id}/approve/${step.id}`, {}); })}>确认步骤</button>}
            </div></li>;
          })}</ol></>}
          {previewRun?.result?.artifacts?.some(isPreview) && <RunPreviewDialog key={previewRun.id}
            title={previewRun.spec.title} runID={previewRun.id} artifacts={previewRun.result.artifacts} token={token} onClose={() => setPreviewRunID('')} />}
          {run && <article id="run-result" className="run-detail" aria-label="运行日志"><div className="line"><h3>{run.spec.title} · {runStatus(run.status, run.result?.detail)}{run.attempt && ` · 第 ${run.attempt} 次`}</h3>{['queued', 'claimed', 'running'].includes(run.status) && <button disabled={busy} onClick={() => void action(async () => { await api(token, `/runs/${run.id}/cancel`, {}); })}>取消执行</button>}</div>
            <RunTiming run={run} now={now} />
            {run.result && <p>{run.result.detail === 'Execution timed out' ? '执行超时' : run.result.detail}{run.result.commit && <><br />Git 提交 <code>{run.result.commit}</code></>}</p>}
            {Boolean(run.result?.bugs?.length) && <section aria-label="QA 缺陷单"><h4>QA 缺陷单</h4><ol>{run.result?.bugs?.map((bug, i) => <li key={i}><strong>{execution?.runs?.find(r => r.id === run.id)?.result?.bugs?.[i]?.id || bug.id || `QA-${run.id.slice(0, 7)}-${i + 1}`} · {bug.title}</strong>
              <p>负责角色　{qaRoles[bug.owner] || bug.owner}</p>
              <p>复现　{bug.steps}</p><p>预期　{bug.expected}</p><p>实际　{bug.actual}</p>
            </li>)}</ol></section>}
            {run.result?.artifacts?.some(item => !isPreview(item) && !item.name.endsWith('.log')) &&
              <ul>{run.result.artifacts.filter(item => !isPreview(item) && !item.name.endsWith('.log')).map(item => <li key={item.name}><button disabled={busy} onClick={() => void action(() => downloadArtifact(run.id, item.name))}>下载 {item.name}</button> <span title={item.sha256}>{Math.ceil(item.bytes / 1024)} KB</span></li>)}</ul>}
            {events.some(event => ['status', 'test', 'error'].includes(event.kind)) &&
              <ol className="summary-events">{events.filter(event => ['status', 'test', 'error'].includes(event.kind)).map(event =>
                <li key={event.seq} data-kind={event.kind}>{event.text}</li>)}</ol>}
            {events.some(event => event.kind === 'qa') && <details><summary>QA 验收报告</summary><pre className="report-text">{events.filter(event => event.kind === 'qa').map(event => event.text).join('\n')}</pre></details>}
          </article>}
        </section>
      </>}
    </main>
  </>;
}
