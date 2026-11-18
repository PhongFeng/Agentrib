'use client';

import { useEffect, useRef, useState } from 'react';
import { api, newID } from './api';

type Message = { role: 'user' | 'assistant'; text: string; draft?: boolean; agent?: string };
type Mode = 'discussion' | 'draft';
type BriefRun = { id: string; status: string; result?: { detail: string; reply?: string } };
type Pending = { id: string; mode: Mode; agent: string };
type SavedChat = { executor: string; skills: string; input: string; messages: Message[]; pending?: Pending };
type BriefChatProps = {
  token: string;
  machine: string;
  project: string;
  online: boolean;
  agents: { id: string; name: string }[];
  onApply: (brief: string) => void;
};

const statusText: Record<string, string> = {
  queued: '等待执行机器领取', claimed: '已领取', running: '正在生成回复',
  canceling: '正在取消', canceled: '已取消', lost: '执行机器连接中断', failed: '产品 Agent 执行失败',
};
const chatStoragePrefix = 'agentrib.brief.';

export function clearBriefChats() {
  for (const key of Object.keys(sessionStorage)) {
    if (key.startsWith(chatStoragePrefix)) sessionStorage.removeItem(key);
  }
}

export function BriefChat({ token, machine, project, online, agents, onApply }: BriefChatProps) {
  const [executor, setExecutor] = useState('');
  const [skills, setSkills] = useState('');
  const [input, setInput] = useState('');
  const [messages, setMessages] = useState<Message[]>([]);
  const [pending, setPending] = useState<Pending>();
  const [status, setStatus] = useState('');
  const [notice, setNotice] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [canceling, setCanceling] = useState(false);
  const [restored, setRestored] = useState(false);
  const lastRequest = useRef<{ key: string; id: string } | undefined>(undefined);
  const storageKey = chatStoragePrefix + machine + '/' + project;

  const agent = agents.find(item => item.id === executor) || agents.find(item => item.id === 'codex') || agents[0];
  const busy = submitting || Boolean(pending);

  useEffect(() => {
    try {
      const raw = sessionStorage.getItem(storageKey);
      if (raw) {
        const saved: SavedChat = JSON.parse(raw);
        setExecutor(saved.executor); setSkills(saved.skills); setInput(saved.input);
        setMessages(saved.messages); setPending(saved.pending);
      }
    } catch {
      sessionStorage.removeItem(storageKey);
    }
    setRestored(true);
  }, [storageKey]);

  useEffect(() => {
    if (!restored) return;
    try {
      sessionStorage.setItem(storageKey, JSON.stringify({ executor, skills, input, messages, pending }));
    } catch {
      // Keep the draft in memory if browser storage is full.
    }
  }, [restored, storageKey, executor, skills, input, messages, pending]);

  useEffect(() => {
    if (!pending) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    const { id, mode, agent } = pending;
    async function refresh() {
      try {
        const { run, events } = await api<{ run: BriefRun; events: { kind: string; text: string }[] }>(token, '/runs/' + id);
        if (disposed) return;
        const reply = run.result?.reply;
        if (run.status === 'succeeded' && reply) {
          setMessages(current => [...current, { role: 'assistant', text: reply, draft: mode === 'draft', agent }]);
          setNotice('');
          setPending(undefined);
          return;
        }
        if (['failed', 'canceled', 'lost', 'succeeded'].includes(run.status)) {
          const detail = [...events].reverse().find(event => event.kind === 'error')?.text || run.result?.detail;
          setNotice((statusText[run.status] || '产品 Agent 没有返回内容') + (detail ? ' · ' + detail : ''));
          setPending(undefined);
          return;
        }
        setStatus(run.status);
        setNotice('');
      } catch (error) {
        if (!disposed) setNotice((error as Error).message);
      }
      if (!disposed) timer = setTimeout(refresh, 1700);
    }
    void refresh();
    return () => { disposed = true; clearTimeout(timer); };
  }, [pending, token]);

  async function send(mode: Mode) {
    const text = input.trim();
    if (!agent || busy || mode === 'discussion' && !text || !messages.length && !text) return;
    const transcript: Message[] = text ? [...messages, { role: 'user', text }] : messages;
    const body = {
      machine_id: machine, project, executor: agent.id, mode,
      skills: agent.id === 'codex' ? skills.trim().split(/[\s,，]+/).filter(Boolean) : [],
      messages: transcript.map(({ role, text }) => ({ role, text })),
    };
    const key = JSON.stringify(body);
    const id = lastRequest.current?.key === key ? lastRequest.current.id : newID();
    lastRequest.current = { key, id };
    setSubmitting(true);
    setNotice('');
    try {
      const run = await api<BriefRun>(token, '/brief-turns', { ...body, id });
      lastRequest.current = undefined;
      setMessages(transcript);
      setInput('');
      setStatus(run.status);
      setPending({ id: run.id, mode, agent: agent.name });
    } catch (error) {
      setNotice((error as Error).message);
    } finally {
      setSubmitting(false);
    }
  }

  async function cancel() {
    if (!pending || canceling) return;
    setCanceling(true);
    try {
      const run = await api<BriefRun>(token, '/runs/' + pending.id + '/cancel', {});
      setStatus(run.status);
    } catch (error) {
      setNotice((error as Error).message);
    } finally {
      setCanceling(false);
    }
  }

  return <details className="brief-chat">
    <summary>与产品 Agent 讨论并生成简报</summary>
    {!online && <p>执行机器离线，消息会在它重新连接后处理。</p>}
    {!agent ? <p>执行机器尚无支持只读执行的 Agent。</p> : <>
      <div className="fields">
        <label>产品 Agent<select value={agent.id} onChange={event => setExecutor(event.target.value)} disabled={busy}>
          {agents.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
        </select></label>
        {agent.id === 'codex' && <label>Codex 技能名（选填）<input value={skills} onChange={event => setSkills(event.target.value)}
          placeholder="填写 Mac mini 上安装的技能名，多个用逗号分隔" disabled={busy} /></label>}
      </div>
      {messages.length > 0 && <ol className="brief-messages" aria-label="产品讨论">{messages.map((message, index) => <li key={index}>
        <strong>{message.role === 'user' ? '你' : message.agent}{message.draft && ' · 简报草稿'}</strong>
        <p>{message.text}</p>
        {message.draft && <button type="button" onClick={() => { onApply(message.text); setNotice('已填入游戏简报，可以继续编辑。'); }}>使用这份简报</button>}
      </li>)}</ol>}
      <label>需求或回复<textarea rows={3} value={input} onChange={event => setInput(event.target.value)} disabled={busy}
        placeholder="描述游戏想法，或这次想改动和验证的内容" /></label>
      <div className="actions">
        <button type="button" onClick={() => void send('discussion')} disabled={busy || !input.trim()}>发送</button>
        <button type="button" className="primary" onClick={() => void send('draft')} disabled={busy || !messages.length && !input.trim()}>生成简报</button>
        {pending && status !== 'canceling' && <button type="button" onClick={() => void cancel()} disabled={canceling}>取消本轮</button>}
        {!busy && messages.length > 0 && <button type="button" className="text-button" onClick={() => { setMessages([]); setInput(''); setNotice(''); lastRequest.current = undefined; }}>新对话</button>}
      </div>
      {(submitting || pending || notice) && <p className="brief-status" role="status">{notice || (submitting ? '正在提交' : statusText[status] || '等待回复')}</p>}
    </>}
  </details>;
}
