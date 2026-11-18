export function newID() { return crypto.randomUUID().replaceAll('-', ''); }

export async function api<T>(token: string, path: string, data?: unknown): Promise<T> {
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
