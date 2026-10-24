// Маленький помощник для построения DOM. Текст всегда вставляется как текст,
// а не как HTML: данные из API не могут превратиться в разметку.

// h('button', { class: 'button', onclick: fn }, 'Текст', child, ...)
// Атрибуты со значением null, undefined или false не ставятся, true ставит
// пустой атрибут. Дети: узлы, строки, числа, массивы; null и false пропускаются.
export function h(tag, props, ...children) {
  const el = document.createElement(tag);
  for (const [name, value] of Object.entries(props ?? {})) {
    if (value == null || value === false) continue;
    if (name === 'class') {
      el.className = value;
    } else if (name.startsWith('on')) {
      el.addEventListener(name.slice(2), value);
    } else {
      el.setAttribute(name, value === true ? '' : String(value));
    }
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const child of children) {
    if (child == null || child === false) continue;
    if (Array.isArray(child)) {
      append(el, child);
    } else {
      el.append(child instanceof Node ? child : String(child));
    }
  }
}

// plural(5, ['товар', 'товара', 'товаров']) → '5 товаров'
export function plural(n, [one, few, many]) {
  const mod10 = n % 10;
  const mod100 = n % 100;
  let word = many;
  if (mod10 === 1 && mod100 !== 11) word = one;
  else if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) word = few;
  return `${n} ${word}`;
}

const dateFormat = new Intl.DateTimeFormat('ru-RU', {
  day: '2-digit',
  month: '2-digit',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
});

const timeFormat = new Intl.DateTimeFormat('ru-RU', {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
});

// Дата и время из API (RFC 3339) в часовом поясе посетителя.
export function formatDate(iso) {
  return dateFormat.format(new Date(iso));
}

export function formatTime(date) {
  return timeFormat.format(date);
}
