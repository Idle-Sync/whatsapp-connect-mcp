var reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

/* ---------- data ---------- */
var AVCOL = ['#5d7a86','#8c6a7f','#7f8c6a','#6a8c85','#8c7a6a','#7a6a8c','#6a758c'];

var CHATS = [
  {k:'office', av:'ST', name:'Studio', prev:'Nikhil: ↪ Photo', time:'13:40', unread:386, cap:412},
  {k:'campus', av:'CS', name:'CSE 2027', prev:'Rahul: ↪ IMG-20260829-WA0063.jpg', time:'13:50', unread:96, cap:118},
  {av:'MA', name:'Ma', prev:'send me the address again beta', time:'13:12', unread:2},
  {av:'B4', name:'Building 4 Society', prev:'Water tanker coming at 6pm', time:'11:04', unread:37},
  {av:'AN', name:'Ankit', prev:'↪ invoice-apr.pdf', time:'10:22', unread:0, tick:1},
  {av:'PR', name:'Priya', prev:'No worries, see you in a bit', time:'09:58', unread:0, tick:1},
  {av:'DV', name:'Dev', prev:'pushed it to the channel', time:'09:31', unread:0, tick:1}
];
var PREVIEWS = {
  office: ['Ria: can we take this to a call','Ops: timesheets. please.','Sam: 😅','Nikhil: ↪ Photo',
           'Meera: sending the deck now','Dev: +1','Priya: I did not'],
  campus: ['Rahul: 😂😂','Sneha: has anyone actually paid','Ishita: ↪ Photo','Aarav: bro',
           'Dept: no extension this time','Rahul: ↪ Video']
};

var FEEDS = {
  office: {
    name:'Studio', initials:'ST', people:'You, Ria, Dev, Sam, Meera, Ops, +28', day:'Today',
    lede:'Four hundred messages landed while you were in a meeting. Somewhere in there is the one thing you actually needed to know.',
    msgs:[
      {w:'Ria', c:0, t:'11:02', x:'morning all ☕'},
      {w:'Dev', c:1, t:'11:04', x:'staging build is up'},
      {w:'Ria', c:0, t:'11:04', x:'link?'},
      {w:'Dev', c:1, t:'11:05', x:'pushed it to the channel'},
      {w:'Nikhil', c:2, t:'11:09', fwd:1, photo:'IMG_4471.jpg'},
      {w:'Sam', c:3, t:'11:10', x:'😂😂'},
      {out:1, t:'11:12', x:'can someone check the type on slide 4'},
      {w:'Sam', c:3, t:'11:13', reply:['You','can someone check the type on slide 4'], x:'on it'},
      {w:'Ops', c:4, t:'11:20', x:'reminder: timesheets are due today'},
      {w:'Dev', c:1, t:'11:47', x:'anyone else getting 502s from staging'},
      {w:'Meera', c:5, t:'12:01', x:'launch moved to the 4th, confirmed with the client'},
      {w:'Sam', c:3, t:'12:01', x:'wait the 4th??'},
      {w:'Dev', c:1, t:'12:02', x:'+1'},
      {w:'Meera', c:5, t:'12:04', x:'yes. copy needs to be with Ria by Tuesday'},
      {w:'Nikhil', c:2, t:'12:11', fwd:1, voice:'0:47'},
      {w:'Ria', c:0, t:'12:14', x:'noted'},
      {w:'Ops', c:4, t:'12:30', x:'also nobody has booked the venue'},
      {w:'Dev', c:1, t:'12:31', x:'I thought Priya did'},
      {w:'Priya', c:3, t:'12:33', x:'I did not'},
      {w:'Nikhil', c:2, t:'12:40', poll:['Standup at 10 or 10:30?', [['10:00',64],['10:30',36]]]},
      {w:'Meera', c:5, t:'13:02', doc:['launch-deck-v7.pdf','PDF · 4.2 MB']},
      {out:1, t:'13:06', x:'in a meeting, will catch up after'},
      {w:'Ops', c:4, t:'13:14', x:'timesheets. please.'},
      {w:'Sam', c:3, t:'13:20', x:'😅'},
      {w:'Ria', c:0, t:'13:31', x:'can we take this to a call'},
      {w:'Nikhil', c:2, t:'13:40', fwd:1, photo:'IMG_4489.jpg'}
    ],
    answer:[
      ['1','Launch moved to the 4th, confirmed with the client.'],
      ['2','Copy has to be with Ria by Tuesday.'],
      ['3','Nobody has booked the venue. Priya thought you had.']
    ],
    foot:'Two people are still waiting on a reply from you.'
  },
  campus: {
    name:'CSE 2027', initials:'CS', people:'You, Aarav, Sneha, Rahul, Ishita, +481', day:'Today',
    lede:'The whole batch is in one group. So is every notice, every meme, and the one message about your exam date.',
    msgs:[
      {w:'Aarav', c:1, t:'09:12', x:'anyone has the DBMS lab manual pdf'},
      {w:'Ishita', c:2, t:'09:14', fwd:1, photo:'notice_scan.jpg'},
      {w:'Rahul', c:3, t:'09:15', x:'😂'},
      {w:'Sneha', c:0, t:'09:20', x:'which lab'},
      {out:1, t:'09:21', x:'DBMS'},
      {w:'Rahul', c:3, t:'09:40', fwd:1, photo:'IMG-20260829-WA0042.jpg'},
      {w:'Dept', c:4, t:'10:05', x:'Mid-sem for DBMS moved to the 9th. Hall 3, 9:30am. Bring your ID.'},
      {w:'Rahul', c:3, t:'10:05', x:'WHAT'},
      {w:'Sneha', c:0, t:'10:06', reply:['Dept','Mid-sem for DBMS moved to the 9th. Hall 3…'], x:'that clashes with the fest'},
      {w:'Ishita', c:2, t:'10:22', x:'😭'},
      {w:'Aarav', c:1, t:'10:51', fwd:1, voice:'1:12'},
      {w:'Dept', c:4, t:'11:15', x:'Fee deadline is the 5th. No extension this time.'},
      {w:'Aarav', c:1, t:'11:16', x:'bro'},
      {w:'Sneha', c:0, t:'11:18', x:'has anyone actually paid'},
      {w:'Rahul', c:3, t:'11:44', fwd:1, photo:'IMG-20260829-WA0051.jpg'},
      {w:'Placement', c:5, t:'12:30', x:'Resume drop closes Friday 6pm. Late entries will not be considered.'},
      {w:'Sneha', c:0, t:'12:31', x:'link?'},
      {w:'Ishita', c:2, t:'12:40', doc:['resume_format_2027.docx','DOCX · 88 KB']},
      {out:1, t:'12:52', x:'was in lab, scrolling up now'},
      {w:'Aarav', c:1, t:'13:20', poll:['Fest afterparty on the 10th?', [['going',71],['not going',29]]]},
      {w:'Ishita', c:2, t:'13:35', x:'anyone actually going to the fest'},
      {w:'Sneha', c:0, t:'13:36', x:'after the 9th apparently'},
      {w:'Rahul', c:3, t:'13:50', fwd:1, photo:'IMG-20260829-WA0063.jpg'}
    ],
    answer:[
      ['1','DBMS mid-sem moved to the 9th, hall 3, 9:30am.'],
      ['2','Fee deadline is the 5th. No extension.'],
      ['3','Placement resume drop closes Friday 6pm.']
    ],
    foot:'Three notices, out of a hundred and eighteen messages.'
  }
};

/* ---------- helpers ---------- */
function el(tag, cls, text){
  var n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
}
var NAMECOL = ['#53bdeb','#e7a26a','#a17bd6','#6fc36f','#e08f8f','#7fd4c1'];
var WAVE = [5,9,14,7,17,11,20,8,13,6,16,10,18,7,12,15,9,19,6,11,8,14];

function bubble(m){
  var row = el('div', 'row' + (m.out ? ' out' : ''));
  var b = el('div', 'bub');
  if (!m.out && m.w){
    var sn = el('span', 'sn', m.w);
    sn.style.color = NAMECOL[(m.c || 0) % 6];
    b.appendChild(sn);
  }
  if (m.fwd) b.appendChild(el('span', 'fwd', '↪ Forwarded many times'));
  if (m.reply){
    var q = el('div', 'quote');
    q.appendChild(el('b', null, m.reply[0]));
    q.appendChild(document.createTextNode(m.reply[1]));
    b.appendChild(q);
  }
  if (m.photo) b.appendChild(el('div', 'photo', m.photo));
  if (m.doc){
    var a = el('div', 'att');
    a.appendChild(el('span', 'ic', m.doc[1].split(' ')[0]));
    var fn = el('div', 'fn');
    fn.appendChild(el('span', null, m.doc[0]));
    fn.appendChild(el('small', null, m.doc[1]));
    a.appendChild(fn);
    b.appendChild(a);
  }
  if (m.voice){
    var wv = el('div', 'wave');
    WAVE.forEach(function(h){ var i = el('i'); i.style.height = h + 'px'; wv.appendChild(i); });
    wv.appendChild(el('span', null, m.voice));
    b.appendChild(wv);
  }
  if (m.poll){
    var pc = el('div', 'pollc');
    pc.appendChild(el('div', 'pq', m.poll[0]));
    m.poll[1].forEach(function(o){
      var box = el('div', 'po');
      var head = el('div');
      head.style.display = 'flex';
      head.style.justifyContent = 'space-between';
      head.appendChild(el('span', null, o[0]));
      head.appendChild(el('span', null, o[1] + '%'));
      box.appendChild(head);
      var bar = el('div', 'pb');
      var fill = el('i');
      fill.style.width = o[1] + '%';
      bar.appendChild(fill);
      box.appendChild(bar);
      pc.appendChild(box);
    });
    b.appendChild(pc);
  }
  if (m.x) b.appendChild(el('span', 'tx', m.x));
  var tm = el('span', 'tm');
  tm.appendChild(el('span', null, m.t));
  if (m.out) tm.appendChild(el('span', 'tk', '✓✓'));
  b.appendChild(tm);
  row.appendChild(b);
  return row;
}

/* ---------- refs ---------- */
var lrows = document.getElementById('lrows'),
    rail = document.getElementById('rail'),
    taphint = document.getElementById('taphint'),
    askrow = document.getElementById('askrow'),
    cbody = document.getElementById('cbody'),
    track = document.getElementById('track'),
    answerEl = document.getElementById('answer'),
    cav = document.getElementById('cav'),
    cname = document.getElementById('cname'),
    cppl = document.getElementById('cppl'),
    ledeEl = document.getElementById('lede'),
    askB = document.getElementById('ask'),
    againB = document.getElementById('again'),
    backB = document.getElementById('back');

/* ---------- screen 1: the list, counts climbing ---------- */
var rowState = {};
CHATS.forEach(function(c, i){
  var r = el('button', 'lrow' + (c.unread ? ' unread' : '') + (c.k ? ' tap' : ''));
  r.type = 'button';
  var av = el('span', 'lav', c.av);
  av.style.background = AVCOL[i % AVCOL.length];
  r.appendChild(av);
  var mid = el('div', 'lmid');
  mid.appendChild(el('div', 'lname', c.name));
  var pv = el('div', 'lprev');
  if (c.tick) pv.appendChild(el('span', 'tk', '✓✓'));
  var pvtx = el('span', null, c.prev);
  pv.appendChild(pvtx);
  mid.appendChild(pv);
  r.appendChild(mid);
  var right = el('div', 'lright');
  var time = el('div', 'ltime', c.time);
  right.appendChild(time);
  var badge = null;
  if (c.unread){
    badge = el('div', 'lbadge', c.unread > 99 ? '99+' : String(c.unread));
    right.appendChild(badge);
  }
  r.appendChild(right);
  if (c.k){
    rowState[c.k] = { row:r, badge:badge, prev:pvtx, n:c.unread, cap:c.cap, p:0 };
    r.addEventListener('click', function(ev){
      var rip = el('span', 'ripple');
      var box = r.getBoundingClientRect();
      rip.style.left = (ev.clientX - box.left) + 'px';
      rip.style.top = (ev.clientY - box.top) + 'px';
      r.appendChild(rip);
      setTimeout(function(){ if (rip.parentNode) rip.parentNode.removeChild(rip); }, 560);
      enter(c.k);
    });
  }
  lrows.appendChild(r);
});

var countT = null, atRest = false;
function countStep(){
  var moved = false;
  ['office','campus'].forEach(function(k){
    var s = rowState[k];
    if (s.n >= s.cap) return;
    // the busy group climbs faster than the quiet one
    s.n += (k === 'office' ? 1 : (Math.random() < 0.55 ? 1 : 0));
    if (s.n > s.cap) s.n = s.cap;
    s.badge.textContent = s.n > 99 ? '99+' : String(s.n);
    s.badge.classList.remove('bump');
    void s.badge.offsetWidth;
    s.badge.classList.add('bump');
    if (Math.random() < 0.4){
      var list = PREVIEWS[k];
      s.p = (s.p + 1) % list.length;
      s.prev.textContent = list[s.p];
    }
    moved = true;
  });
  if (moved){
    countT = setTimeout(countStep, 260 + Math.round(Math.random() * 300));
  } else {
    // it has run out of room. let it rest, and invite the tap.
    countT = null; atRest = true;
    rowState.office.row.classList.add('ready');
    taphint.classList.add('on');
  }
}
function startCounting(){
  if (entered || atRest || countT !== null) return;
  countT = setTimeout(countStep, 900);
}
function stopCounting(){ if (countT !== null){ clearTimeout(countT); countT = null; } }
function resetCounts(){
  atRest = false;
  taphint.classList.remove('on');
  ['office','campus'].forEach(function(k){
    var s = rowState[k];
    s.n = k === 'office' ? 386 : 96;
    s.badge.textContent = s.n > 99 ? '99+' : String(s.n);
    s.row.classList.remove('ready');
  });
}

/* ---------- screen 2: the flood, no controls inside ---------- */
var flavour = 'office', cursor = 0, timer = null, burst = 0, entered = false;

function slideIn(node){
  if (reduced) return;
  var h = node.offsetHeight + 5;
  track.style.transition = 'none';
  track.style.transform = 'translateY(' + h + 'px)';
  void track.offsetHeight;
  track.style.transition = 'transform .26s cubic-bezier(.2,.8,.3,1)';
  track.style.transform = 'translateY(0)';
}
function trim(){ while (track.children.length > 26) track.removeChild(track.firstChild); }
function push(m){
  var row = bubble(m);
  if (!reduced){
    row.classList.add('pop');
    setTimeout(function(){ row.classList.remove('pop'); }, 320);
  }
  track.appendChild(row);
  slideIn(row);
  trim();
}
function showTyping(){
  var row = el('div', 'row typing');
  var b = el('div', 'bub');
  b.appendChild(el('i')); b.appendChild(el('i')); b.appendChild(el('i'));
  row.appendChild(b);
  track.appendChild(row);
  slideIn(row);
  trim();
  return row;
}
function gap(m){
  if (burst > 0){ burst -= 1; return 170 + Math.round(Math.random() * 130); }
  var len = (m.x || '').length || 20;
  return 340 + Math.min(len * 12, 720) + Math.round(Math.random() * 240);
}
function tick(){
  var f = FEEDS[flavour];
  var m = f.msgs[cursor % f.msgs.length];
  cursor += 1;
  var wantsTyping = !reduced && burst === 0 && !m.out && !m.fwd && (m.x || '').length > 26 && cursor % 4 === 0;
  if (wantsTyping){
    var t = showTyping();
    timer = setTimeout(function(){
      if (t.parentNode) track.removeChild(t);
      push(m);
      timer = setTimeout(tick, gap(m));
    }, 760);
  } else {
    push(m);
    timer = setTimeout(tick, gap(m));
  }
}
function startFeed(){ if (entered && timer === null) timer = setTimeout(tick, 260); }
function stopFeed(){ if (timer !== null){ clearTimeout(timer); timer = null; } }

function buildAnswer(f){
  answerEl.innerHTML = '';
  answerEl.appendChild(el('p', 'ah', 'what you missed'));
  f.answer.forEach(function(a){
    var r = el('div', 'al');
    r.appendChild(el('i', null, a[0]));
    r.appendChild(el('span', null, a[1]));
    answerEl.appendChild(r);
  });
  answerEl.appendChild(el('p', 'af', f.foot));
}

function enter(key){
  var f = FEEDS[key];
  flavour = key;
  entered = true;
  stopCounting();
  cbody.classList.remove('answered');
  cav.textContent = f.initials;
  cav.style.background = AVCOL[key === 'office' ? 0 : 1];
  cname.textContent = f.name;
  cppl.textContent = f.people;
  ledeEl.textContent = f.lede;
  track.innerHTML = '';
  track.style.transform = 'translateY(0)';
  track.appendChild(el('div', 'daychip', f.day));
  for (var i = 0; i < 4; i++) track.appendChild(bubble(f.msgs[i]));
  track.appendChild(el('div', 'unreadchip', rowState[key].n + ' unread messages'));
  cursor = 4;
  burst = 7;
  buildAnswer(f);
  rail.classList.add('in');
  taphint.classList.remove('on');
  rowState[key].row.classList.remove('ready');
  setTimeout(function(){ askrow.classList.add('on'); }, 900);
  stopFeed();
  startFeed();
}

function leave(){
  stopFeed();
  entered = false;
  cbody.classList.remove('answered');
  rail.classList.remove('in');
  askrow.classList.remove('on');
  ledeEl.textContent = 'It sits at the top of the list with a number on it that only goes up. Open it. See how far you get.';
  resetCounts();
  startCounting();
}

askB.addEventListener('click', function(){
  // the flood does not stop. you just stop having to read it.
  cbody.classList.add('answered');
});
againB.addEventListener('click', leave);
backB.addEventListener('click', leave);

var phoneIO = new IntersectionObserver(function(entries){
  entries.forEach(function(en){
    if (en.isIntersecting){ startCounting(); startFeed(); }
    else { stopCounting(); stopFeed(); }
  });
}, { threshold: 0.25 });
phoneIO.observe(document.getElementById('phone'));

(function(){
  var c = document.getElementById('clock');
  function set(){
    var d = new Date();
    c.textContent = String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0');
  }
  set(); setInterval(set, 20000);
})();

/* ---------- beat 2: the agent doing the work ---------- */
var SCENES = [
  {
    chip: 'What did I miss?',
    str: [0, 3],
    q: 'What did I miss in Studio today?',
    calls: [
      ['list_messages', 'chat: Studio · window: today', '412 messages'],
      ['get_message_context', 'around 3 decisions · ±5', '9 messages'],
      ['get_last_interaction', 'unanswered', '2 people']
    ],
    lines: [
      ['lead', 'Three things actually happened.'],
      ['item', '1', 'Launch moved to the 4th, confirmed with the client.'],
      ['item', '2', 'Copy has to be with Ria by Tuesday.'],
      ['item', '3', 'Nobody booked the venue. Priya thought you had.'],
      ['tailnote', 'Ria and Ma are both still waiting on a reply from you.']
    ]
  },
  {
    chip: 'Find those invoices',
    str: [1, 2],
    q: 'Find the invoices Ankit sent me after April',
    calls: [
      ['search_messages', 'query: invoice · chat: Ankit · after: 2026-04-01', '3 hits'],
      ['download_media', 'message ids: 8f21c4, b0d913', '2 files saved']
    ],
    lines: [
      ['lead', 'Three, two of them with attachments.'],
      ['item', '·', 'Apr 22 — invoice-apr.pdf, ₹48,000, saved to ~/whatsapp/media'],
      ['item', '·', 'Jun 02 — quote-r2.pdf, revised after your call'],
      ['tailnote', 'The April one is never marked paid anywhere in the thread.']
    ]
  },
  {
    chip: 'Who am I ignoring?',
    str: [3],
    q: 'Who messaged me that I never replied to?',
    calls: [
      ['list_chats', 'newest first · 7 chats', '7 chats'],
      ['get_last_interaction', 'per chat · unanswered only', '2 people']
    ],
    lines: [
      ['lead', 'Two people, both today.'],
      ['item', '·', 'Ma asked for the address again at 13:12. Four hours ago.'],
      ['item', '·', 'Priya answered your question at 12:33 and you never came back.'],
      ['tailnote', 'Nothing older than a day is outstanding.']
    ]
  },
  {
    chip: 'Reply to Ma',
    str: [1, 4],
    q: 'Send Ma the address she asked for',
    calls: [
      ['search_messages', 'query: address · chat: Ma · before: 2026-01-01', '1 hit · Dec 14'],
      ['get_message_context', 'around Dec 14 · ±3', '6 messages'],
      ['send_message', 'chat: Ma · 41 characters', 'held', 1]
    ],
    lines: [
      ['lead', 'Found it. She sent it herself on 14 December, then you both forgot.'],
      ['held', 'to Ma', '“Flat 402, Sunview, Sector 9 — the one you sent me in December 🙂”',
        'The agent wrote it. It cannot send it. The server is holding this until you say yes.']
    ]
  }
];

var STRENGTHS = [
  ['Reaches back years, not the last twenty messages', 'fetch_older_messages'],
  ['Date filters WhatsApp has never given you', 'search_messages'],
  ['Pulls attachments onto disk where they can be read', 'download_media'],
  ['Sees every chat at once, groups included', 'list_chats'],
  ['Can act for you — but only through the gate', 'send_message · gated']
];

var chipWrap = document.getElementById('qchips'),
    umsg = document.getElementById('umsg'),
    callsEl = document.getElementById('calls'),
    atext = document.getElementById('atext'),
    awpill = document.getElementById('awpill'),
    awsend = document.getElementById('awsend'),
    askhint = document.getElementById('askhint'),
    sceneTimers = [], running = false, chips = [];

function later(ms, fn){ sceneTimers.push(setTimeout(fn, reduced ? Math.min(ms, 40) : ms)); }
function clearScene(){ sceneTimers.forEach(clearTimeout); sceneTimers = []; }

function idleWindow(){
  umsg.textContent = '';
  umsg.classList.remove('on');
  callsEl.innerHTML = '';
  atext.innerHTML = '';
  awpill.textContent = 'Ask about your messages';
  awpill.classList.remove('live');
  awsend.classList.remove('armed','fire');
}
function invite(text, which){
  askhint.textContent = text;
  askhint.classList.add('on');
  chips.forEach(function(c, n){ c.classList.toggle('ready', n === which); });
}
function clearInvite(){
  askhint.classList.remove('on');
  chips.forEach(function(c){ c.classList.remove('ready'); });
}

/* you type it, the same way you tapped the group */
function typeQuestion(text, done){
  awpill.classList.add('live');
  awpill.textContent = '';
  var i = 0;
  if (reduced){ awpill.textContent = text; awsend.classList.add('armed'); done(); return; }
  function step(){
    i += 1;
    awpill.textContent = text.slice(0, i);
    if (i >= text.length){
      awsend.classList.add('armed');
      later(360, function(){
        awsend.classList.add('fire');
        later(150, function(){ awsend.classList.remove('fire'); done(); });
      });
      return;
    }
    sceneTimers.push(setTimeout(step, 16 + Math.round(Math.random() * 34)));
  }
  sceneTimers.push(setTimeout(step, 180));
}

function runScene(i){
  var s = SCENES[i];
  awpill.textContent = 'Ask about your messages';
  awpill.classList.remove('live');
  awsend.classList.remove('armed');
  umsg.textContent = s.q;
  callsEl.innerHTML = '';
  atext.innerHTML = '';
  later(60, function(){ umsg.classList.add('on'); });

  var t = 520, heldCall = null;
  s.calls.forEach(function(c){
    var row = el('div', 'tcall');
    row.appendChild(el('span', 'sp'));
    row.appendChild(el('span', 'nm', c[0]));
    row.appendChild(el('span', 'args', c[1]));
    var res = el('span', 'res', '');
    row.appendChild(res);
    callsEl.appendChild(row);
    if (c[3]) heldCall = { row: row, res: res };
    later(t, function(){ row.classList.add('on'); });
    later(t + 760, function(){ row.classList.add(c[3] ? 'held' : 'done'); res.textContent = c[2]; });
    t += 550;
  });

  t += 500;
  later(t - 140, function(){ lightStrengths(s.str); });
  var caretHost = null;
  s.lines.forEach(function(l, n){
    var ln = el('div', 'ln ' + l[0]);
    if (l[0] === 'item'){
      ln.appendChild(el('i', null, l[1]));
      ln.appendChild(el('span', null, l[2]));
    } else if (l[0] === 'held'){
      var hh = el('div', 'hh');
      hh.appendChild(el('span', 'b'));
      var hlab = el('span', null, 'held at the gate · ' + l[1]);
      hh.appendChild(hlab);
      ln.appendChild(hh);
      ln.appendChild(el('div', 'body2', l[2]));
      ln.appendChild(el('div', 'why', l[3]));

      var res2 = el('div', 'hres');
      var acts = el('div', 'hacts');
      var ok = el('button', 'ok', 'Approve and send');
      var no = el('button', 'nope', 'Not now');
      ok.type = 'button'; no.type = 'button';

      ok.addEventListener('click', function(){
        var now = new Date();
        var stamp = String(now.getHours()).padStart(2,'0') + ':' + String(now.getMinutes()).padStart(2,'0');
        ln.classList.add('approved');
        hlab.textContent = 'approved by you · sent ' + stamp;
        res2.textContent = 'Delivered to Ma at ' + stamp + '. One tap, and only because it was yours to give.';
        if (heldCall){
          heldCall.row.classList.remove('held');
          heldCall.row.classList.add('done');
          heldCall.res.textContent = 'sent';
        }
        stEls[4].classList.remove('gated');
      });
      no.addEventListener('click', function(){
        ln.classList.add('denied');
        hlab.textContent = 'denied · it never left your machine';
        res2.textContent = 'Nothing was sent. The draft is gone and Ma never knew there was one.';
        if (heldCall) heldCall.res.textContent = 'denied';
      });

      acts.appendChild(ok);
      acts.appendChild(no);
      ln.appendChild(acts);
      ln.appendChild(res2);
    } else {
      ln.textContent = l[1];
    }
    atext.appendChild(ln);
    later(t + n * 250, function(){
      ln.classList.add('on');
      if (caretHost) caretHost.remove();
      if (n < s.lines.length - 1){
        caretHost = el('span', 'caret');
        ln.appendChild(caretHost);
      } else {
        caretHost = null;
      }
    });
  });

  var ends = t + s.lines.length * 250 + 500;
  later(ends, function(){
    running = false;
    chips.forEach(function(c){ c.disabled = false; });
    var next = (i + 1) % SCENES.length;
    invite('ask it something else', next);
  });
}

function ask(i){
  if (running) return;
  running = true;
  clearScene();
  clearInvite();
  chips.forEach(function(c, n){
    c.disabled = true;
    c.setAttribute('aria-pressed', String(n === i));
  });
  idleWindow();
  typeQuestion(SCENES[i].q, function(){ runScene(i); });
}

SCENES.forEach(function(s, i){
  var b = el('button', 'qchip', s.chip);
  b.type = 'button';
  b.setAttribute('aria-pressed', 'false');
  b.addEventListener('click', function(){ ask(i); });
  chipWrap.appendChild(b);
  chips.push(b);
});

/* the strengths light up as each demo earns them */
var stWrap = document.getElementById('strengths'), stEls = [];
STRENGTHS.forEach(function(s, i){
  var d = el('div', 'st' + (i === 4 ? ' gated' : ''));
  d.appendChild(el('span', 'no', (i + 1 < 10 ? '0' : '') + (i + 1)));
  d.appendChild(el('span', 'tx', s[0]));
  d.appendChild(el('span', 'tool', s[1]));
  stWrap.appendChild(d);
  stEls.push(d);
});
function lightStrengths(list){
  stEls[4].classList.add('gated');   // back to amber until you approve again
  stEls.forEach(function(d, i){ d.classList.toggle('lit', (list || []).indexOf(i) !== -1); });
}

idleWindow();

(function(){
  var armedOnce = false;
  var io = new IntersectionObserver(function(e){
    if (e[0].isIntersecting && !armedOnce){
      armedOnce = true;
      setTimeout(function(){ if (!running) invite('pick one and watch it work', 0); }, 900);
    }
  }, { threshold: 0.28 });
  io.observe(document.querySelector('.appwin'));
})();

/* ---------- beat 4 packets ---------- */
(function(){
  if (reduced) return;
  var a = document.getElementById('pk1'), b = document.getElementById('pk2'), t0 = null;
  function fr(t){
    if (t0 === null) t0 = t;
    var e = ((t - t0) / 2500) % 1;
    a.setAttribute('cx', String(106 + e * 100));
    a.setAttribute('opacity', String(Math.sin(e * Math.PI)));
    var f = ((t - t0 + 1250) / 2500) % 1;
    b.setAttribute('cx', String(402 + f * 110));
    b.setAttribute('opacity', String(Math.sin(f * Math.PI)));
    requestAnimationFrame(fr);
  }
  requestAnimationFrame(fr);
})();

/* ---------- beat 7 install ---------- */
var SNIP = {
  unix: '<span class="cm"># download, pair, wire up your clients</span>\ncurl -fsSL https://raw.githubusercontent.com/idle-sync/whatsapp-connect-mcp/main/scripts/install.sh | sh',
  win:  '<span class="cm"># PowerShell</span>\nirm https://raw.githubusercontent.com/idle-sync/whatsapp-connect-mcp/main/scripts/install.ps1 | iex',
  npm:  '<span class="cm"># a global install keeps the binary at a stable path</span>\nnpm install -g whatsapp-connect-mcp\nwhatsapp-connect-mcp setup\n\n<span class="cm"># using the http transport? start the server too</span>\nwhatsapp-connect-mcp serve <span class="fl">--http</span> 127.0.0.1:2178'
};
var RAW = {
  unix: 'curl -fsSL https://raw.githubusercontent.com/idle-sync/whatsapp-connect-mcp/main/scripts/install.sh | sh',
  win:  'irm https://raw.githubusercontent.com/idle-sync/whatsapp-connect-mcp/main/scripts/install.ps1 | iex',
  npm:  'npm install -g whatsapp-connect-mcp\nwhatsapp-connect-mcp setup'
};
var codebox = document.getElementById('codebox'), current = 'unix';
var tabs = document.querySelectorAll('.tabs button');
function pick(k){
  current = k;
  codebox.innerHTML = SNIP[k];
  tabs.forEach(function(b){ b.setAttribute('aria-selected', String(b.dataset.t === k)); });
}
tabs.forEach(function(b){ b.addEventListener('click', function(){ pick(b.dataset.t); }); });
if (navigator.platform && /Win/i.test(navigator.platform)) pick('win'); else pick('unix');

var copyb = document.getElementById('copyb'), copyT;
copyb.addEventListener('click', function(){
  if (navigator.clipboard) navigator.clipboard.writeText(RAW[current]).catch(function(){});
  copyb.textContent = 'copied';
  clearTimeout(copyT);
  copyT = setTimeout(function(){ copyb.textContent = 'copy'; }, 1400);
});

/* ---------- reveals ---------- */
(function(){
  var io = new IntersectionObserver(function(entries){
    entries.forEach(function(en){
      if (en.isIntersecting){ en.target.classList.add('in'); io.unobserve(en.target); }
    });
  }, { rootMargin: '0px 0px -70px 0px' });
  document.querySelectorAll('.rv').forEach(function(n){ io.observe(n); });
})();
