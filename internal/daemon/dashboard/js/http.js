export async function responseError(response) {
  const text = await response.text();
  try {
    const message = JSON.parse(text).error?.message;
    if (typeof message === "string" && message) return Error(message);
  } catch {}
  return Error(text || `HTTP ${response.status}`);
}

export async function signinRequest(path, options = {}) {
  const response = await fetch(path, { ...options, headers: { "content-type": "application/json", ...(options.headers || {}) } });
  const text = await response.text();
  let body = {};
  try {
    body = text ? JSON.parse(text) : {};
  } catch {
    body = {};
  }
  if (!response.ok) throw Error(body.error?.message || text || `HTTP ${response.status}`);
  return body;
}
