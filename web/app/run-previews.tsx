'use client';

import { useEffect, useRef, useState } from 'react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

export type Artifact = { name: string; sha256: string; bytes: number };

const formats: Record<string, string> = {
  md: 'text/markdown', png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp',
  wav: 'audio/wav', ogg: 'audio/ogg', mp3: 'audio/mpeg', flac: 'audio/flac', m4a: 'audio/mp4', opus: 'audio/ogg',
};

export function isPreview(artifact: Artifact) {
  return /^preview-\d+-/.test(artifact.name) && Boolean(kind(artifact));
}

function kind(artifact: Artifact) {
  const extension = artifact.name.split('.').pop() || '';
  return Object.hasOwn(formats, extension) ? formats[extension] : '';
}

function fileName(artifact: Artifact) {
  return artifact.name.replace(/^preview-\d+-/, '');
}

export function previewLabel(artifacts?: Artifact[]) {
  const item = artifacts?.find(isPreview);
  const mime = item && kind(item);
  if (mime === 'text/markdown') return '查看产品文档';
  if (mime?.startsWith('image/')) return '查看美术';
  if (mime?.startsWith('audio/')) return '试听音效';
}

function PreviewFile({ runID, artifact, token }: { runID: string; artifact: Artifact; token: string }) {
  const mime = kind(artifact);
  const name = fileName(artifact);
  const audio = mime.startsWith('audio/');
  const [requested, setRequested] = useState(!audio);
  const [value, setValue] = useState('');
  const [error, setError] = useState('');
  useEffect(() => {
    if (!requested) return;
    const abort = new AbortController();
    let url = '';
    async function load() {
      try {
        const response = await fetch(`/api/v1/runs/${runID}/artifacts/${encodeURIComponent(artifact.name)}`, {
          headers: { Authorization: 'Bearer ' + token }, signal: abort.signal, cache: 'no-store',
        });
        if (!response.ok) throw new Error();
        const content = mime === 'text/markdown' ? await response.text() : await response.arrayBuffer();
        if (abort.signal.aborted) return;
        if (typeof content === 'string') setValue(content);
        else {
          url = URL.createObjectURL(new Blob([content], { type: mime }));
          setValue(url);
        }
      } catch {
        if (!abort.signal.aborted) setError('预览加载失败');
      }
    }
    void load();
    return () => {
      abort.abort();
      if (url) URL.revokeObjectURL(url);
    };
  }, [requested, runID, artifact.name, token, mime]);

  if (error) return <p role="status">{error}</p>;
  if (!requested) return <button onClick={() => setRequested(true)}>播放</button>;
  if (!value) return <p role="status">正在加载 {name}…</p>;
  if (mime === 'text/markdown') {
    return <div className="markdown-preview"><Markdown remarkPlugins={[remarkGfm]} skipHtml disallowedElements={['img', 'a']} unwrapDisallowed>{value}</Markdown></div>;
  }
  if (audio) return <audio aria-label={name} src={value} controls autoPlay onError={() => setError('浏览器无法播放此音频')} />;
  return <img src={value} alt={name} loading="lazy" onError={() => setError('图片无法显示')} />;
}

function RunPreviews({ runID, artifacts, token }: { runID: string; artifacts: Artifact[]; token: string }) {
  const previews = artifacts.filter(isPreview);
  const docs = previews.filter(file => kind(file) === 'text/markdown');
  const media = previews.filter(file => kind(file) !== 'text/markdown');
  const [document, setDocument] = useState('');
  const active = docs.find(file => file.name === document) || docs.find(file => fileName(file) === 'requirements.md') || docs[0];
  return <section aria-label="Agent 产出">
    {active && <>
      <label>产品文档<select value={active.name} onChange={event => setDocument(event.target.value)}>
        {docs.map(file => <option key={file.name} value={file.name}>{fileName(file)}</option>)}
      </select></label>
      <PreviewFile key={active.name} runID={runID} artifact={active} token={token} />
    </>}
    {media.length > 0 && <div className="asset-previews">{media.map(file => <figure key={file.name}>
      <figcaption title={fileName(file)}>{fileName(file)}</figcaption>
      <PreviewFile runID={runID} artifact={file} token={token} />
    </figure>)}</div>}
  </section>;
}

export function RunPreviewDialog({ title, runID, artifacts, token, onClose }: {
  title: string; runID: string; artifacts: Artifact[]; token: string; onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    if (dialog.current && !dialog.current.open) dialog.current.showModal();
  }, []);
  return <dialog ref={dialog} className="preview-dialog" aria-label={title} onClose={onClose}>
    <div className="line preview-heading">
      <h3>{title}</h3>
      <button type="button" onClick={() => dialog.current?.close()}>关闭</button>
    </div>
    <div className="preview-body"><RunPreviews runID={runID} artifacts={artifacts} token={token} /></div>
  </dialog>;
}
