// Тренировочное демо для GitHub Pages: подменяет fetch для /api/*,
// данные живут в localStorage, PIN 0000. Файл подключается до app-скрипта.
(function () {
  'use strict';
  const PIN = '0000';
  const KEY = 'hg_demo_v1';
  const EV = 'Демо-мероприятие';
  const ARCHIVE = 'Архив (демо)';
  const realFetch = window.fetch.bind(window);

  function ean13(n) {
    const base = ('46006070000' + n).slice(-12);
    let s = 0;
    for (let i = 0; i < 12; i++) s += +base[i] * (i % 2 ? 3 : 1);
    return base + ((10 - (s % 10)) % 10);
  }

  function seed() {
    const rows = [
      ['Анна Петрова', 'VIP', '+7 900 111-11-11'],
      ['Борис Смирнов', 'Стандарт', '+7 900 222-22-22'],
      ['Вера Соколова', 'Стандарт', '+7 900 333-33-33'],
      ['Георгий Ким', 'Пресса', '+7 900 444-44-44'],
      ['Дарья Иванова', 'VIP', '+7 900 555-55-55'],
      ['Егор Волков', 'Стандарт', '+7 900 666-66-66'],
      ['Жанна Морозова', 'Стандарт', '+7 900 777-77-77'],
      ['Захар Лебедев', 'Стандарт', '+7 900 888-88-88'],
      ['Ирина Новикова', 'Пресса', '+7 900 999-99-99'],
      ['Кирилл Егоров', 'Стандарт', '+7 900 000-00-00'],
      ['Лариса Павлова', 'VIP', '+7 900 123-45-67'],
      ['Максим Степанов', 'Стандарт', '+7 900 765-43-21'],
    ];
    const now = Date.now();
    const at = min => new Date(now - min * 60000).toISOString();
    const guests = rows.map((r, i) => ({
      id: 'g' + (i + 1),
      name: r[0],
      email: 'demo' + (i + 1) + '@example.com',
      phone: r[2],
      ticket_type: r[1],
      ticket_number: String(10001 + i),
      barcode: ean13(i + 1),
      source: 'timepad',
      event: EV,
      arrived_at: [0, 3, 6].includes(i) ? at(20 - i * 5) : '',
    }));
    guests.push({
      id: 'a1', name: 'Ольга Из Архива', phone: '+7 900 555-00-00',
      ticket_type: 'Стандарт', ticket_number: '20001', barcode: ean13(90),
      source: 'timepad', event: ARCHIVE, arrived_at: '',
    });
    return {
      events: [{ event: EV, hidden: false }, { event: ARCHIVE, hidden: true }],
      guests,
      via: { [EV]: { g1: 'tap', g4: 'qr', g7: 'tap' } },
      qrmiss: {},
    };
  }

  function db() {
    let d;
    try { d = JSON.parse(localStorage.getItem(KEY)); } catch (e) { d = null; }
    if (!d || !d.guests) { d = seed(); save(d); }
    return d;
  }
  function save(d) { localStorage.setItem(KEY, JSON.stringify(d)); }

  function stats(d, ev) {
    const gs = d.guests.filter(g => g.event === ev);
    const via = d.via[ev] || {};
    const st = { event: ev, total: gs.length, arrived: 0, qr: 0, tap: 0, walkins: 0, false_qr: d.qrmiss[ev] || 0 };
    for (const g of gs) {
      if (g.source === 'walkin') st.walkins++;
      if (g.arrived_at) {
        st.arrived++;
        if (via[g.id] === 'qr') st.qr++; else st.tap++;
      }
    }
    return st;
  }

  function json(v, status) {
    return new Response(JSON.stringify(v), {
      status: status || 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }

  function header(init, name) {
    const h = (init && init.headers) || {};
    if (typeof h.get === 'function') return h.get(name) || '';
    return h[name] || h[name.toLowerCase()] || '';
  }

  async function route(input, init) {
    const u = new URL(input, location.href);
    const p = u.pathname.replace(/^.*(\/api\/)/, '$1');
    const method = ((init && init.method) || 'GET').toUpperCase();
    const pin = header(init, 'X-Pin');
    const body = () => { try { return JSON.parse((init && init.body) || '{}'); } catch (e) { return {}; } };

    if (p === '/api/version') return json({ version: 'demo' });
    if (p === '/api/pin') return pin === PIN ? json({ ok: true }) : json({ error: 'bad pin' }, 401);
    if (pin !== PIN) return json({ error: 'bad pin' }, 401);

    const d = db();
    const ev = u.searchParams.get('event') || '';
    const b = body();

    if (p === '/api/events' && method === 'GET')
      return json(d.events.map(e => {
        const s = stats(d, e.event);
        return { event: e.event, total: s.total, arrived: s.arrived, hidden: e.hidden };
      }));
    if (p === '/api/state' && method === 'GET')
      return json({ guests: d.guests.filter(g => g.event === ev) });
    if (p === '/api/stats' && method === 'GET') return json(stats(d, ev));

    if (p === '/api/checkin' && method === 'POST') {
      const g = d.guests.find(x => x.event === b.event && x.id === b.guest_id);
      if (!g) return json({ error: 'guest not found' }, 404);
      const already = !!g.arrived_at;
      if (!already) {
        g.arrived_at = new Date().toISOString();
        (d.via[b.event] = d.via[b.event] || {})[g.id] = b.via || 'tap';
      }
      save(d);
      return json({ ok: true, already });
    }
    if (p === '/api/undo' && method === 'POST') {
      const g = d.guests.find(x => x.event === b.event && x.id === b.guest_id);
      if (!g) return json({ error: 'guest not found' }, 404);
      g.arrived_at = '';
      if (d.via[b.event]) delete d.via[b.event][g.id];
      save(d);
      return json({ ok: true });
    }
    if (p === '/api/guest' && method === 'POST') {
      if (!b.name) return json({ error: 'name required' }, 400);
      b.id = b.id || 'walkin:' + Math.random().toString(36).slice(2, 10);
      b.source = 'walkin';
      d.guests.push(b);
      save(d);
      return json({ ok: true, id: b.id });
    }
    if (p === '/api/qrmiss' && method === 'POST') {
      d.qrmiss[b.event] = (d.qrmiss[b.event] || 0) + 1;
      save(d);
      return json({ ok: true });
    }
    if (p === '/api/event/rename' && method === 'POST') {
      const e = d.events.find(x => x.event === b.event);
      if (!e) return json({ error: 'not found' }, 404);
      d.guests.forEach(g => { if (g.event === b.event) g.event = b.new; });
      if (d.via[b.event]) { d.via[b.new] = d.via[b.event]; delete d.via[b.event]; }
      if (d.qrmiss[b.event]) { d.qrmiss[b.new] = d.qrmiss[b.event]; delete d.qrmiss[b.event]; }
      e.event = b.new;
      save(d);
      return json({ ok: true });
    }
    if (p === '/api/event/hide' && method === 'POST') {
      const e = d.events.find(x => x.event === b.event);
      if (!e) return json({ error: 'not found' }, 404);
      e.hidden = !!b.hidden;
      save(d);
      return json({ ok: true });
    }
    if (p === '/api/import' || p === '/api/refresh')
      return json({ error: 'В демо-режиме импорт недоступен' }, 400);

    return json({ error: 'not found' }, 404);
  }

  window.fetch = function (input, init) {
    if (typeof input !== 'string' || !/\/api\//.test(input)) return realFetch(input, init);
    return route(input, init || {});
  };

  addEventListener('load', () => setTimeout(() => {
    const s = document.getElementById('pinScreen');
    if (s && !s.classList.contains('hide') && !document.getElementById('demoHint')) {
      const p = document.createElement('p');
      p.id = 'demoHint';
      p.style.cssText = 'color:#8a94a0;text-align:center;font-size:14px';
      p.textContent = 'Демонстрационный режим · PIN 0000';
      s.appendChild(p);
    }
  }, 300));
})();
