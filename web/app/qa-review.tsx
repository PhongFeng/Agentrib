'use client';

import { useState } from 'react';

export const qaRoles: Record<string, string> = { art: '美术', audio: '音效', develop: '开发', environment: '执行环境', producer: '产品' };
export type Bug = { id?: string; owner: string; title: string; steps: string; expected: string; actual: string };
export type Ticket = Bug & { id: string; closed: boolean; helpers?: string[]; deferred_reason?: string };
type Assignment = { owner: string; helpers?: string[] };
export type Review = { phase: string; reason: string; run_id: string; roles?: string[]; assignments?: Record<string, Assignment>; environment_tickets?: string[] };

type Props = {
  tickets: Ticket[]; review?: Review; bugs: Bug[]; busy: boolean;
  decide: (body: object) => void;
};

export function QAReview({ tickets, review, bugs, busy, decide }: Props) {
  const [choices, setChoices] = useState<Record<string, Assignment>>({});
  const [note, setNote] = useState('');
  const [stop, setStop] = useState(false);
  const [deferral, setDeferral] = useState({ id: '', reason: '' });
  const roles = review?.roles || [];
  const work = bugs.filter(bug => bug.owner !== 'environment' && bug.id);
  const plan = Object.fromEntries(work.map(bug => [bug.id!, choices[bug.id!] || review?.assignments?.[bug.id!] || { owner: roles.includes(bug.owner) ? bug.owner : '' }]));
  const product = review?.phase === 'product_review';
  const environment = review?.phase === 'environment';
  const closed = tickets.filter(ticket => ticket.closed).length;
  const deferred = tickets.filter(ticket => ticket.deferred_reason).length;
  return <section aria-label="缺陷跟踪">
    {review && <p role="status"><strong>{review.reason}</strong></p>}
    <details open={product || environment || undefined}><summary>缺陷跟踪 · {tickets.length - closed - deferred} 张待验 · {closed} 张已验证{deferred > 0 && ` · ${deferred} 张已延期`}</summary>
      <ul>{tickets.map(ticket => {
        const assignment = review?.assignments?.[ticket.id];
        const owners = assignment ? [assignment.owner, ...(assignment.helpers || [])] : [ticket.owner, ...(ticket.helpers || [])];
        return <li key={ticket.id}><strong>{ticket.id} · {ticket.title}</strong>
          <p>{owners.map(role => qaRoles[role]).join(' / ')} · {ticket.closed ? 'QA 已验证' : ticket.deferred_reason ? '已延期 · 未验证' : ticket.owner === 'environment' ? '待环境补验' : '待 QA 验证'}</p>
          {ticket.deferred_reason && <p>延期理由　{ticket.deferred_reason}</p>}
          {environment && review?.environment_tickets?.includes(ticket.id) && (deferral.id === ticket.id ? <div>
            <p>该项本轮保留未验证，其余验收项继续按原要求验证。</p>
            <label>延期理由<textarea value={deferral.reason} maxLength={900} onChange={event => setDeferral({ id: ticket.id, reason: event.target.value })} /></label>
            <div className="actions">
              <button disabled={busy || !deferral.reason.trim()} onClick={() => decide({ action: 'defer_ticket', ticket_id: ticket.id, note: deferral.reason.trim() })}>确认延期</button>
              <button onClick={() => setDeferral({ id: '', reason: '' })}>取消</button>
            </div>
          </div> : <button disabled={busy} onClick={() => setDeferral({ id: ticket.id, reason: '' })}>延期处理</button>)}
        </li>;
      })}</ul>
    </details>
    {product && <div>{work.map(bug => {
      const id = bug.id!;
      const choice = plan[id];
      return <fieldset key={id}><legend>{id} · {bug.title}</legend>
        <p>{bug.actual}（预期：{bug.expected}）</p>
        <label>主责<select aria-label={`${id} 主责`} value={choice.owner} onChange={event => {
          const owner = event.target.value;
          setChoices({ ...choices, [id]: { owner, helpers: choice.helpers?.filter((role: string) => role !== owner) } });
        }}><option value="">选择主责</option>{roles.map(role => <option key={role} value={role}>{qaRoles[role]}</option>)}</select></label>
        {roles.filter(role => role !== choice.owner).map(role => <label className="check" key={role}><input type="checkbox"
          checked={choice.helpers?.includes(role) || false} onChange={event => {
            const helpers = choice.helpers || [];
            setChoices({ ...choices, [id]: { ...choice, helpers: event.target.checked ? [...helpers, role] : helpers.filter((r: string) => r !== role) } });
          }} />{qaRoles[role]}协作</label>)}
      </fieldset>;
    })}
      <label>产品说明<textarea value={note} maxLength={900} onChange={event => setNote(event.target.value)} /></label>
      <div className="actions"><button disabled={busy || !work.length || Object.values(plan).some(a => !a.owner)} onClick={() => decide({ action: 'assign', assignments: plan, note })}>确认分派并返工</button>
        <button disabled={busy || !note.trim()} onClick={() => decide({ action: 'recheck', note })}>按补充说明复验当前构建</button></div>
    </div>}
    {environment && <button disabled={busy} onClick={() => decide({ action: 'environment_ready' })}>环境已恢复，开始补验</button>}
    {(product || environment) && (stop ? <div className="actions"><span>终止本次工作流及其待执行任务？</span><button disabled={busy} onClick={() => decide({ action: 'stop' })}>确认终止</button><button onClick={() => setStop(false)}>返回</button></div>
      : <button disabled={busy} onClick={() => setStop(true)}>终止本次工作流</button>)}
  </section>;
}
