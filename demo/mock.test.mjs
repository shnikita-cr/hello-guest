// Прогон демо-API без браузера: node demo/mock.test.mjs
import { readFileSync } from 'node:fs';
import assert from 'node:assert/strict';

globalThis.window = globalThis;
const mem = new Map();
globalThis.localStorage = {
  getItem: k => (mem.has(k) ? mem.get(k) : null),
  setItem: (k, v) => mem.set(k, String(v)),
  removeItem: k => mem.delete(k),
};
globalThis.location = { href: 'https://shnikita-cr.github.io/hello-guest/' };
globalThis.addEventListener = globalThis.addEventListener || (() => {});

eval(readFileSync(new URL('./mock.js', import.meta.url), 'utf8'));

const api = async (path, opt = {}) => {
  const r = await window.fetch(path, {
    ...opt,
    headers: { 'X-Pin': opt.pin ?? '0000', ...(opt.headers || {}) },
  });
  return [r.status, await r.json()];
};

// PIN
assert.deepEqual(await api('/api/pin', { pin: '9999' }), [401, { error: 'bad pin' }]);
assert.deepEqual(await api('/api/pin'), [200, { ok: true }]);
assert.deepEqual(await api('/api/version'), [200, { version: 'demo' }]);
assert.equal((await api('/api/events', { pin: '9999' }))[0], 401);

// список событий и состояние
let [st, evs] = await api('/api/events');
assert.equal(st, 200);
const main = evs.find(e => e.event === 'Демо-мероприятие');
assert.equal(main.total, 12);
assert.equal(main.arrived, 3);
assert.equal(evs.find(e => e.hidden).hidden, true);

[st, evs] = await api('/api/state?event=' + encodeURIComponent('Демо-мероприятие'));
const gs = evs.guests;
assert.equal(gs.length, 12);
assert.match(gs[0].barcode, /^\d{13}$/);
assert.ok(gs.find(g => g.id === 'g1').arrived_at, 'g1 должен быть отмечен');

// команды из seed
const boris = gs.find(g => g.id === 'g2');
assert.equal(boris.ticket_type, 'Командный билет');
assert.equal(boris.team, 'Орбита');
assert.equal(boris.members.length, 3);
assert.equal(boris.member_arrived['Смирнов Борис Олегович'], true, 'капитан пришёл');
assert.equal(boris.member_arrived['Гущин Роман Львович'], false, 'третий ещё не пришёл');
assert.equal(gs.filter(g => g.team).length, 3, 'три команды в seed');
const egors = gs.find(g => g.id === 'g6');
assert.equal(egors.members.length, 4);
assert.equal(gs.find(g => g.id === 'g11').team, 'Тёплый стан');

// отметка и повтор
const checkin = body => api('/api/checkin', { method: 'POST', body: JSON.stringify(body) });
const EV = 'Демо-мероприятие';
assert.deepEqual(await checkin({ event: EV, guest_id: 'g2', via: 'qr' }), [200, { ok: true, already: false }]);
assert.deepEqual(await checkin({ event: EV, guest_id: 'g2', via: 'qr' }), [200, { ok: true, already: true }]);
assert.deepEqual(await checkin({ event: EV, guest_id: 'nope' }), [404, { error: 'guest not found' }]);

let stats = (await api('/api/stats?event=' + encodeURIComponent(EV)))[1];
assert.equal(stats.arrived, 4);
assert.equal(stats.qr, 2); // g4 + g2
assert.equal(stats.tap, 2); // g1 + g7
assert.equal(stats.walkins, 0);
assert.equal(stats.false_qr, 0);

// снятие отметки
await api('/api/undo', { method: 'POST', body: JSON.stringify({ event: EV, guest_id: 'g2' }) });
stats = (await api('/api/stats?event=' + encodeURIComponent(EV)))[1];
assert.equal(stats.arrived, 3);
assert.equal(stats.qr, 1);

// поимённая отметка членов команды
const member = members => api('/api/member', { method: 'POST', body: JSON.stringify({ event: EV, guest_id: 'g6', members }) });
assert.deepEqual(await member({ 'Волков Егор Дмитриевич': true, 'Шаров Пётр Игоревич': true, 'Лапина Ольга Романовна': false, 'Кузьмин Артём Вадимович': false }), [200, { ok: true }]);
assert.deepEqual(await member(null), [200, { ok: true }], 'null сбрасывает отметки');
assert.deepEqual(await member({ 'Волков Егор Дмитриевич': true, 'Шаров Пётр Игоревич': true, 'Лапина Ольга Романовна': false, 'Кузьмин Артём Вадимович': false }), [200, { ok: true }]);
assert.deepEqual(await api('/api/member', { method: 'POST', body: JSON.stringify({ event: EV, guest_id: 'nope', members: {} }) }), [404, { error: 'guest not found' }]);
let persisted = (await api('/api/state?event=' + encodeURIComponent(EV)))[1].guests.find(g => g.id === 'g6');
assert.equal(persisted.member_arrived['Шаров Пётр Игоревич'], true, 'member_arrived сохранился в состоянии');

// прогулочный гость
const [gst, add] = await api('/api/guest', { method: 'POST', body: JSON.stringify({ event: EV, name: 'Прохожий', ticket_type: 'Стандарт' }) });
assert.equal(gst, 200);
assert.match(add.id, /^walkin:/);
await checkin({ event: EV, guest_id: add.id, via: 'tap' });
stats = (await api('/api/stats?event=' + encodeURIComponent(EV)))[1];
assert.equal(stats.walkins, 1);
assert.equal(stats.total, 13);

// ненайденный код
await api('/api/qrmiss', { method: 'POST', body: JSON.stringify({ event: EV, code: 'X' }) });
stats = (await api('/api/stats?event=' + encodeURIComponent(EV)))[1];
assert.equal(stats.false_qr, 1);

// импорт в демо запрещён
const [im, imp] = await api('/api/import?event=' + encodeURIComponent(EV), { method: 'POST', body: 'xx' });
assert.equal(im, 400);
assert.match(imp.error, /демо/i);

// переименование уносит гостей
await api('/api/event/rename', { method: 'POST', body: JSON.stringify({ event: 'Архив (демо)', new: 'Архив 2026' }) });
evs = (await api('/api/events'))[1];
assert.ok(evs.some(e => e.event === 'Архив 2026'));
assert.equal((await api('/api/state?event=' + encodeURIComponent('Архив 2026')))[1].guests.length, 1);

console.log('demo mock: все проверки прошли');
