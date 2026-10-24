// Вызовы API. Каждый запрос, удачный или нет, попадает в журнал запросов на
// странице: фронтенд показывает API, а не прячет его.

export class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

const listeners = new Set();

// onRequest(fn): fn получает запись журнала после каждого запроса.
export function onRequest(fn) {
  listeners.add(fn);
}

let lastID = 0;

// request('POST', '/receptions', { token, body }) → тело ответа (JSON).
// Ответ не 2xx → ApiError с текстом из {"message"} контракта.
export async function request(method, path, { token, body } = {}) {
  const entry = {
    id: ++lastID,
    at: new Date(),
    method,
    path,
    withToken: Boolean(token),
    requestBody: body,
  };
  const headers = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (token) headers.Authorization = `Bearer ${token}`;

  const started = performance.now();
  let response;
  let text;
  try {
    response = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    text = await response.text();
  } catch {
    entry.ms = Math.round(performance.now() - started);
    entry.status = 0;
    entry.responseBody = 'нет ответа: сервер недоступен';
    emit(entry);
    throw new ApiError(0, 'Сервер недоступен, проверьте соединение');
  }
  entry.ms = Math.round(performance.now() - started);
  entry.status = response.status;
  entry.responseBody = parse(text);
  emit(entry);

  if (!response.ok) {
    const message = entry.responseBody?.message ?? `HTTP ${response.status}`;
    throw new ApiError(response.status, message);
  }
  return entry.responseBody;
}

function parse(text) {
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function emit(entry) {
  for (const fn of listeners) fn(entry);
}
