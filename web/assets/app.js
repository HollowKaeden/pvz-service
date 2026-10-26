// Фронтенд демо ПВЗ-сервиса. Всё, что делает посетитель, — запросы к API из
// контракта (api/openapi.yaml); своих ручек у фронтенда нет.

import { ApiError, onRequest, request } from './api.js';
import { formatDate, formatTime, h, plural } from './dom.js';

// Значения enum'ов из контракта: справочники не меняются без миграции и
// новой версии контракта.
const CITIES = ['Москва', 'Санкт-Петербург', 'Казань'];
const PRODUCT_TYPES = ['электроника', 'одежда', 'обувь'];
const LIMITS = [5, 10, 30];

const ROLES = {
  employee: {
    name: 'сотрудник ПВЗ',
    can: 'Можно открывать и закрывать приёмки, добавлять и удалять товары.',
  },
  moderator: {
    name: 'модератор',
    can: 'Можно заводить ПВЗ. Приёмками занимается сотрудник: чтобы их попробовать, смените роль.',
  },
};

const SESSION_KEY = 'pvz.session';
const LOG_LIMIT = 50;

const state = {
  role: null,
  token: null,
  email: null, // вход по email; null — вход по роли через /dummyLogin
  filter: { start: '', end: '' }, // даты в формате <input type="date">
  page: 1,
  limit: 10,
  items: null, // текущая страница GET /pvz; null — ещё не загружена
  expanded: new Set(), // id раскрытых ПВЗ
  busy: false, // идёт запрос: кнопки действий неактивны
  listMessage: null, // { text, error } над списком
  pvzMessage: null, // { id, text, error } в карточке ПВЗ
};

const $ = (id) => document.getElementById(id);

// --- сессия ---------------------------------------------------------------

// Токен живёт до закрытия вкладки (sessionStorage). Хранилище может быть
// недоступно (приватный режим, запрет cookies) — тогда просто без сессии.
function loadSession() {
  try {
    const saved = JSON.parse(sessionStorage.getItem(SESSION_KEY));
    if (saved?.token && ROLES[saved.role]) {
      state.role = saved.role;
      state.token = saved.token;
      state.email = saved.email ?? null;
    }
  } catch {
    // нет сессии
  }
}

function saveSession() {
  try {
    if (state.token) {
      sessionStorage.setItem(SESSION_KEY, JSON.stringify({ role: state.role, token: state.token, email: state.email }));
    } else {
      sessionStorage.removeItem(SESSION_KEY);
    }
  } catch {
    // сессия не сохранится, страница продолжит работать
  }
}

// Вход по роли без пароля: POST /dummyLogin.
async function dummyLogin(role) {
  const token = await authRequest('/dummyLogin', { role });
  if (token) await signedIn(token, role, null);
}

// Вход по email: POST /login. Роли в ответе нет, она записана в самом токене.
async function emailLogin(email, password) {
  const token = await authRequest('/login', { email, password });
  if (!token) return;
  const role = tokenRole(token);
  if (!ROLES[role]) {
    showRoleError(new Error('В токене нет известной роли'));
    return;
  }
  await signedIn(token, role, email);
}

// Регистрация: POST /register заводит пользователя, но токена не выдаёт. Его
// выдаёт следующий за ним POST /login, оба запроса видны в журнале.
async function register(email, password, role) {
  const user = await authRequest('/register', { email, password, role });
  if (user) await emailLogin(user.email, password);
}

// authRequest — запрос входа или регистрации. Пока он идёт, кнопки входа
// неактивны; ошибка показывается под ними, результат null.
async function authRequest(path, body) {
  setAuthBusy(true);
  try {
    return await request('POST', path, { body });
  } catch (err) {
    showRoleError(err);
    return null;
  } finally {
    setAuthBusy(false);
  }
}

function setAuthBusy(busy) {
  for (const button of document.querySelectorAll('#roles button, #email-form button')) {
    button.disabled = busy;
  }
}

// Роль из токена. JWT — три части в base64url через точку, роль лежит во
// второй (payload). Подпись здесь не проверяется: это делает сервер на каждом
// запросе.
function tokenRole(token) {
  try {
    const payload = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
    return JSON.parse(atob(payload)).role;
  } catch {
    return null;
  }
}

async function signedIn(token, role, email) {
  state.token = token;
  state.role = role;
  state.email = email;
  saveSession();
  state.page = 1;
  state.listMessage = null;
  state.pvzMessage = null;
  renderRole();
  renderPVZCard();
  await loadList();
}

function logout(message) {
  state.role = null;
  state.token = null;
  state.email = null;
  state.items = null;
  state.expanded.clear();
  saveSession();
  renderRole(message);
  renderPVZCard();
}

// API ответил 401: токен истёк (живёт сутки) или подпись сменилась.
function handleAuthError(err) {
  if (err instanceof ApiError && err.status === 401) {
    logout('Токен больше не действует. Войдите заново.');
    return true;
  }
  return false;
}

// --- данные ---------------------------------------------------------------

function listQuery() {
  const q = new URLSearchParams({ page: state.page, limit: state.limit });
  // Границы дня в часовом поясе посетителя, в API — моменты времени.
  if (state.filter.start) q.set('startDate', new Date(`${state.filter.start}T00:00:00`).toISOString());
  if (state.filter.end) q.set('endDate', new Date(`${state.filter.end}T23:59:59.999`).toISOString());
  return q;
}

async function loadList() {
  if (!state.token) return;
  state.busy = true;
  renderList();
  try {
    state.items = await request('GET', `/pvz?${listQuery()}`, { token: state.token });
  } catch (err) {
    if (handleAuthError(err)) return;
    state.listMessage = { text: errorText(err), error: true };
  } finally {
    state.busy = false;
  }
  renderList();
}

async function createPVZ(city) {
  let pvz;
  state.busy = true;
  renderList();
  try {
    pvz = await request('POST', '/pvz', { token: state.token, body: { city } });
  } catch (err) {
    state.busy = false;
    if (handleAuthError(err)) return;
    state.listMessage = { text: errorText(err), error: true };
    renderList();
    return;
  }
  // Новый ПВЗ без приёмок: под фильтр по дате он не попадёт, поэтому фильтр
  // сбрасывается, и показывается первая страница (новые сверху).
  state.filter = { start: '', end: '' };
  state.page = 1;
  state.expanded.add(pvz.id);
  state.listMessage = { text: `ПВЗ в городе ${pvz.city} заведён.` };
  renderPVZCard();
  await loadList();
}

// Действия сотрудника с приёмкой ПВЗ: кнопка → запрос из контракта.
const ACTIONS = {
  open: (id) => ['POST', '/receptions', { pvzId: id }, 'Приёмка открыта.'],
  add: (id, type) => ['POST', '/products', { pvzId: id, type }, `Товар «${type}» добавлен.`],
  deleteLast: (id) => ['POST', `/pvz/${id}/delete_last_product`, undefined, 'Последний товар удалён.'],
  close: (id) => ['POST', `/pvz/${id}/close_last_reception`, undefined, 'Приёмка закрыта.'],
};

async function act(pvzID, action, arg) {
  const [method, path, body, done] = ACTIONS[action](pvzID, arg);
  state.busy = true;
  renderList();
  try {
    await request(method, path, { token: state.token, body });
    state.pvzMessage = { id: pvzID, text: done };
  } catch (err) {
    state.busy = false;
    if (handleAuthError(err)) return;
    state.pvzMessage = { id: pvzID, text: errorText(err), error: true };
  }
  // Отдельной ручки «один ПВЗ» в контракте нет: обновляется вся страница.
  await loadList();
}

function errorText(err) {
  if (err instanceof ApiError && err.status > 0) return `Ошибка ${err.status}: ${err.message}`;
  return err.message;
}

// --- роль -----------------------------------------------------------------

function renderRole(message) {
  // Кнопка роли отмечена, только если вход был через неё, а не по email.
  for (const button of $('roles').querySelectorAll('button')) {
    button.setAttribute('aria-pressed', String(!state.email && button.dataset.role === state.role));
  }
  const box = $('role-state');
  if (!state.role) {
    box.replaceChildren(message ? h('p', { class: 'message error' }, message) : '');
    return;
  }
  const role = ROLES[state.role];
  box.replaceChildren(
    h('p', null, `Вы вошли как ${role.name}${state.email ? ` (${state.email})` : ''}. `, h('span', { class: 'muted' }, role.can)),
    h('button', { type: 'button', class: 'text-button', onclick: () => logout() }, 'Выйти'),
  );
}

function showRoleError(err) {
  $('role-state').replaceChildren(h('p', { class: 'message error' }, errorText(err)));
}

// --- карточка ПВЗ ---------------------------------------------------------

// Каркас карточки меняется только со сменой роли: формы не перерисовываются
// на каждый запрос и не теряют фокус.
function renderPVZCard() {
  const box = $('pvz');
  if (!state.token) {
    box.replaceChildren(
      h('p', { class: 'muted' }, 'Войдите: список ПВЗ (GET /pvz) доступен обеим ролям, но только с токеном.'),
    );
    return;
  }
  // replaceChildren превратил бы null в текст «null», поэтому пустое убирается.
  const parts = [
    state.role === 'moderator' ? createForm() : null,
    filterForm(),
    h('div', { id: 'list-message', role: 'status' }),
    h('ul', { id: 'pvz-list', class: 'pvz-list' }),
    h('div', { id: 'pager', class: 'pager' }),
  ];
  box.replaceChildren(...parts.filter(Boolean));
  renderList();
}

function createForm() {
  const select = h('select', { name: 'city', class: 'input' }, CITIES.map((c) => h('option', null, c)));
  return h(
    'form',
    {
      class: 'panel',
      onsubmit: (e) => {
        e.preventDefault();
        createPVZ(select.value);
      },
    },
    h('label', { class: 'field' }, h('span', { class: 'label' }, 'Новый ПВЗ'), select),
    h('button', { class: 'button primary', 'data-busy': true }, 'Завести ПВЗ'),
  );
}

function filterForm() {
  const start = h('input', { type: 'date', class: 'input', value: state.filter.start });
  const end = h('input', { type: 'date', class: 'input', value: state.filter.end });
  const limit = h(
    'select',
    { class: 'input' },
    LIMITS.map((n) => h('option', { value: n, selected: n === state.limit }, n)),
  );
  const apply = () => {
    state.filter = { start: start.value, end: end.value };
    state.limit = Number(limit.value);
    state.page = 1;
    state.listMessage = null;
    loadList();
  };
  return h(
    'form',
    {
      class: 'panel filter',
      onsubmit: (e) => {
        e.preventDefault();
        apply();
      },
    },
    h('label', { class: 'field' }, h('span', { class: 'label' }, 'Приёмки с'), start),
    h('label', { class: 'field' }, h('span', { class: 'label' }, 'по'), end),
    h('label', { class: 'field' }, h('span', { class: 'label' }, 'На странице'), limit),
    h(
      'div',
      { class: 'buttons' },
      h('button', { class: 'button primary', 'data-busy': true }, 'Показать'),
      h(
        'button',
        {
          type: 'button',
          class: 'button',
          'data-busy': true,
          onclick: () => {
            start.value = '';
            end.value = '';
            apply();
          },
        },
        'Сбросить',
      ),
    ),
    h(
      'p',
      { class: 'hint muted' },
      'С фильтром видны только ПВЗ, где были приёмки в эти дни, и только эти приёмки.',
    ),
  );
}

function renderList() {
  const list = $('pvz-list');
  if (!list) return;

  $('list-message').replaceChildren(message(state.listMessage));
  for (const button of document.querySelectorAll('#pvz [data-busy]')) {
    button.disabled = state.busy;
  }

  if (state.items === null) {
    list.replaceChildren(h('li', { class: 'muted' }, 'Загрузка…'));
    $('pager').replaceChildren();
    return;
  }
  const filtered = Boolean(state.filter.start || state.filter.end);
  list.replaceChildren(
    ...(state.items.length
      ? state.items.map(renderItem)
      : [
          h(
            'li',
            { class: 'muted' },
            filtered ? 'В эти дни приёмок не было.' : state.page > 1 ? 'На этой странице пусто.' : 'ПВЗ пока нет.',
          ),
        ]),
  );
  renderPager();
}

function renderPager() {
  // Общего числа ПВЗ контракт не отдаёт: следующая страница есть, если
  // текущая заполнена целиком.
  const go = (page) => {
    state.page = page;
    state.listMessage = null;
    loadList();
  };
  $('pager').replaceChildren(
    h(
      'button',
      { type: 'button', class: 'button', disabled: state.busy || state.page === 1, onclick: () => go(state.page - 1) },
      '← Назад',
    ),
    h('span', { class: 'muted' }, `Страница ${state.page}`),
    h(
      'button',
      {
        type: 'button',
        class: 'button',
        disabled: state.busy || state.items.length < state.limit,
        onclick: () => go(state.page + 1),
      },
      'Дальше →',
    ),
  );
}

function renderItem({ pvz, receptions }) {
  const open = receptions.find((r) => r.reception.status === 'in_progress');
  const products = receptions.reduce((n, r) => n + r.products.length, 0);
  const expanded = state.expanded.has(pvz.id);
  const toggle = () => {
    if (expanded) state.expanded.delete(pvz.id);
    else state.expanded.add(pvz.id);
    renderList();
  };

  return h(
    'li',
    { class: 'pvz' },
    h(
      'button',
      { type: 'button', class: 'pvz-head', 'aria-expanded': String(expanded), onclick: toggle },
      h(
        'span',
        { class: 'pvz-title' },
        h('span', { class: 'pvz-city' }, pvz.city),
        open ? h('span', { class: 'badge' }, 'приёмка открыта') : null,
      ),
      h(
        'span',
        { class: 'muted' },
        `с ${formatDate(pvz.registrationDate)} · ${plural(receptions.length, ['приёмка', 'приёмки', 'приёмок'])} · ${plural(products, ['товар', 'товара', 'товаров'])}`,
      ),
    ),
    expanded ? renderDetails(pvz, receptions, open) : null,
  );
}

function renderDetails(pvz, receptions, open) {
  return h(
    'div',
    { class: 'pvz-body' },
    h('p', { class: 'muted small' }, `id ${pvz.id}`),
    state.role === 'employee'
      ? renderActions(pvz.id, open)
      : h('p', { class: 'muted' }, 'Приёмками занимается сотрудник ПВЗ: смените роль, чтобы открыть приёмку и добавить товары.'),
    state.pvzMessage?.id === pvz.id ? message(state.pvzMessage) : null,
    receptions.length
      ? h('ol', { class: 'receptions' }, receptions.map(renderReception))
      : h('p', { class: 'muted' }, 'Приёмок пока не было.'),
  );
}

function renderActions(id, open) {
  const button = (text, action, arg, primary = false, disabled = false) =>
    h(
      'button',
      {
        type: 'button',
        class: primary ? 'button primary' : 'button',
        'data-busy': true,
        disabled: state.busy || disabled,
        onclick: () => act(id, action, arg),
      },
      text,
    );

  if (!open) {
    return h('div', { class: 'buttons' }, button('Открыть приёмку', 'open', null, true));
  }
  return h(
    'div',
    { class: 'actions' },
    h('div', { class: 'buttons' }, PRODUCT_TYPES.map((type) => button(`+ ${type}`, 'add', type, true))),
    h(
      'div',
      { class: 'buttons' },
      button('Удалить последний', 'deleteLast', null, false, open.products.length === 0),
      // Пустую приёмку сервис закрыть не даст (400), кнопка неактивна заранее.
      button('Закрыть приёмку', 'close', null, false, open.products.length === 0),
    ),
  );
}

function renderReception({ reception, products }) {
  const isOpen = reception.status === 'in_progress';
  return h(
    'li',
    { class: isOpen ? 'reception open' : 'reception' },
    h(
      'div',
      { class: 'reception-head' },
      h('span', null, isOpen ? 'Открытая приёмка' : 'Закрытая приёмка'),
      h('span', { class: 'muted' }, `${formatDate(reception.dateTime)} · ${plural(products.length, ['товар', 'товара', 'товаров'])}`),
    ),
    products.length
      ? h(
          'ol',
          { class: 'products' },
          products.map((p, i) =>
            h(
              'li',
              { class: 'tag', 'data-type': p.type, title: `добавлен ${formatDate(p.dateTime)}` },
              `${i + 1}. ${p.type}`,
            ),
          ),
        )
      : null,
  );
}

function message(msg) {
  if (!msg) return '';
  return h('p', { class: msg.error ? 'message error' : 'message' }, msg.text);
}

// --- журнал запросов ------------------------------------------------------

function logEntry(entry) {
  const failed = entry.status === 0 || entry.status >= 400;
  const request = [];
  if (entry.withToken) request.push('Authorization: Bearer <токен>');
  // Пароль в журнал не попадает: журнал на экране, его могут показывать.
  const body = entry.requestBody?.password === undefined ? entry.requestBody : { ...entry.requestBody, password: '********' };
  if (body !== undefined) request.push(JSON.stringify(body, null, 2));

  $('log-empty').hidden = true;
  const log = $('log');
  log.prepend(
    h(
      'li',
      null,
      h(
        'details',
        null,
        h(
          'summary',
          { class: 'log-line' },
          h('span', { class: 'muted' }, formatTime(entry.at)),
          h('span', { class: 'log-request' }, `${entry.method} ${decodeURIComponent(entry.path)}`),
          h('span', { class: failed ? 'status failed' : 'status ok' }, entry.status || 'нет'),
          h('span', { class: 'muted' }, `${entry.ms} мс`),
        ),
        h(
          'div',
          { class: 'log-body' },
          request.length ? [h('p', { class: 'label' }, 'Запрос'), h('pre', null, request.join('\n'))] : null,
          h('p', { class: 'label' }, 'Ответ'),
          h(
            'pre',
            null,
            entry.responseBody === null
              ? '(пустое тело)'
              : typeof entry.responseBody === 'string'
                ? entry.responseBody
                : JSON.stringify(entry.responseBody, null, 2),
          ),
        ),
      ),
    ),
  );
  while (log.children.length > LOG_LIMIT) log.lastElementChild.remove();
}

// --- gRPC -----------------------------------------------------------------

// Команда grpcurl для GetPVZList. На демо gRPC идёт через тот же адрес, что и
// страница (Caddy отдаёт его pvz по HTTP/2), локально — на порт 3000 без TLS.
// Reflection включён, поэтому proto-файл grpcurl не нужен.
function showGrpcurl() {
  const method = 'pvz.v1.PVZService/GetPVZList';
  const command =
    location.protocol === 'https:'
      ? `grpcurl ${location.hostname}:443 ${method}`
      : `grpcurl -plaintext ${location.hostname}:3000 ${method}`;
  $('grpcurl').textContent = command;

  const copy = $('grpcurl-copy');
  copy.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(command);
      copy.textContent = 'Скопировано';
    } catch {
      copy.textContent = 'Не удалось скопировать';
    }
    setTimeout(() => {
      copy.textContent = 'Скопировать';
    }, 2000);
  });
}

// --- старт ----------------------------------------------------------------

function init() {

  // Ссылка на портфолио: демо живёт на поддомене pvz. основного сайта.
  // Локально (localhost) ссылки нет.
  const host = location.hostname;
  if (host.startsWith('pvz.')) {
    const link = $('portfolio-link');
    link.href = `https://${host.slice(4)}/`;
    link.hidden = false;
  }

  showGrpcurl();
  onRequest(logEntry);
  $('log-clear').addEventListener('click', () => {
    $('log').replaceChildren();
    $('log-empty').hidden = false;
  });
  for (const button of $('roles').querySelectorAll('button')) {
    button.addEventListener('click', () => dummyLogin(button.dataset.role));
  }
  const form = $('email-form');
  form.addEventListener('submit', (e) => {
    e.preventDefault();
    emailLogin(form.elements.email.value.trim(), form.elements.password.value);
  });
  $('register').addEventListener('click', () => {
    register(form.elements.email.value.trim(), form.elements.password.value, form.elements.role.value);
  });

  loadSession();
  renderRole();
  renderPVZCard();
  loadList();
}

init();
