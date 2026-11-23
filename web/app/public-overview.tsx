'use client';

import { useEffect, useState } from 'react';

type Overview = {
  machines: number;
  online_machines: number;
  projects: number;
  recent: { project: string; created: string; status: string }[];
};

const date = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short', hour12: false });

export function PublicOverview({ statusBadge }: { statusBadge: (status: string) => React.ReactNode }) {
  const [overview, setOverview] = useState<Overview>();
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const response = await fetch('/api/v1/public/overview', {
          credentials: 'omit', cache: 'no-store', signal: controller.signal,
        });
        if (!response.ok) throw new Error();
        const data: Overview = await response.json();
        if (!controller.signal.aborted) { setOverview(data); setFailed(false); }
      } catch {
        if (!controller.signal.aborted) setFailed(true);
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(refresh, 15000);
      }
    }
    void refresh();
    return () => { controller.abort(); clearTimeout(timer); };
  }, []);

  return <>
    <div className="line overview-heading"><h1>制作概览</h1><span className="state">访客 · 只读</span></div>
    {failed && <p className="notice" role="status">{overview ? '更新失败，当前显示上次读取的数据。' : '暂时无法读取制作状态。'}稍后将自动重试。</p>}
    {overview ? <>
      <section className="panel overview-stats" aria-label="工作室概况">
        <div><h2>在线执行机器</h2><strong>{overview.online_machines}<small> / {overview.machines} 台</small></strong></div>
        <div><h2>游戏项目</h2><strong>{overview.projects}<small> 个</small></strong></div>
      </section>
      <section className="panel">
        <h2>近期制作记录</h2>
        {overview.recent.length ? <ol className="machine-inventory">{overview.recent.map((item, i) =>
          <li className="line" key={item.created + i}>
            <div><strong>{item.project}</strong><p>发起于 <time dateTime={item.created}>{date.format(new Date(item.created))}</time></p></div>
            {statusBadge(item.status)}
          </li>)}</ol> : <p>暂无制作记录</p>}
      </section>
    </> : !failed && <p className="panel" role="status">正在读取制作状态…</p>}
  </>;
}
