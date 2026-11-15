<script>
"use strict";
const $=s=>document.querySelector(s);
const store={get(k,d){try{return localStorage.getItem(k)??d}catch(e){return d}},set(k,v){try{localStorage.setItem(k,v)}catch(e){}}};
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const isMobile=()=>matchMedia('(max-width:900px)').matches;
let S=null,W=null,MET=null,TRF=null,FLOW=null,EVS=null,DEVS=null;
let page='home';
// Окно один для всех баллов — то же, по которому решает сторож (scoreWindow = 6 ч).
// Статистика за 1 ч / 24 ч — только в подробностях сервера (кнопка «i»).
const win='w6';
const winSec=()=>21600, winTxt=()=>'6 ч';

// ---------- тема и эффекты ----------
function applyTheme(){
  document.body.classList.toggle('simple',store.get('uiMode','simple')!=='expert');
  let t=store.get('theme','xrock');if(t==='unifi'){t='xrock';store.set('theme',t)} document.documentElement.dataset.theme=t;
  const fxPref=store.get('fx','auto'); const full=fxPref==='full'||(fxPref==='auto'&&!isMobile()&&matchMedia('(pointer:fine)').matches);
  document.body.classList.toggle('fx',full);
  $('meta[name=theme-color]').content=t==='srm'?'#e9eef4':t==='unifi'?'#131417':'#07070d';
}
const fxOn=()=>document.body.classList.contains('fx');

// ---------- мелочи ----------
function toast(t){const el=$('#toast');el.textContent=t;el.classList.add('on');clearTimeout(el._t);el._t=setTimeout(()=>el.classList.remove('on'),2600)}
async function api(path,body,method){
  const r=await fetch(path,body!==undefined?{method:method||'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}:{cache:'no-store'});
  const ct=r.headers.get('content-type')||''; const j=ct.includes('json')?await r.json().catch(()=>({})):{error:await r.text()};
  if(r.status===401&&j.login){location.href='/login';throw new Error('нужен вход')}
  if(!r.ok) throw new Error(j.error||('HTTP '+r.status)); return j;
}
function splitFlag(tag){const c=[...(tag||'')];if(c.length>=2){const a=c[0].codePointAt(0),b=c[1].codePointAt(0);if(a>=0x1F1E6&&a<=0x1F1FF&&b>=0x1F1E6&&b<=0x1F1FF)return{flag:c[0]+c[1],cc:String.fromCharCode(a-0x1F1E6+65,b-0x1F1E6+65),name:c.slice(2).join('').trim()}}return{flag:'',cc:'',name:tag||''}}
function srvName(tag,withProto){const {flag,name}=splitFlag(tag);let n=name,p='';const m=name.match(/^(.*?)\s*\(([^)]*)\)\)?$/);if(m){n=m[1];p=m[2]}return `${flag?`<span class="fl">${flag}</span> `:''}${esc(n)}${withProto&&p?` <span class="n" style="font-weight:400">${esc(p)}</span>`:''}`}
function ago(t){if(!t)return'—';const s=Math.max(0,Math.round(Date.now()/1000-t));return s<60?s+' с назад':s<3600?Math.round(s/60)+' мин назад':s<86400?Math.round(s/3600)+' ч назад':Math.round(s/86400)+' дн назад'}
function dur(s){s=Math.max(0,Math.round(s));const d=Math.floor(s/86400),h=Math.floor(s%86400/3600),m=Math.floor(s%3600/60);return d?`${d} д ${h} ч`:h?`${h} ч ${m} мин`:`${m} мин`}
function tm(t,sec){return new Date(t*1000).toLocaleTimeString('ru-RU',{hour:'2-digit',minute:'2-digit',second:sec?'2-digit':undefined})}
function gb(b){b=b||0;return b>=1073741824?(b/1073741824).toFixed(b>=10737418240?0:1)+' ГБ':b>=1048576?(b/1048576).toFixed(0)+' МБ':(b/1024).toFixed(0)+' КБ'}
function mbps(k){return (k/1000).toFixed(k<10000?1:0)}
const col=v=>v>=75?'var(--good)':v>=50?'var(--mid)':'var(--bad)';
const clsMs=d=>d<250?'g':d<600?'m':'b';

// ---------- графики (SVG) ----------
function area(data,color,h,opts={}){
  const W2=300,H=h||42; if(!data||data.length<2) return `<svg viewBox="0 0 ${W2} ${H}" preserveAspectRatio="none" style="height:${H}px"></svg>`;
  const vals=data.map(v=>v??0), mx=(opts.max??Math.max(...vals,1e-9))*1.12, mn=opts.min??0;
  const pts=vals.map((v,i)=>[(i/(vals.length-1)*W2).toFixed(1),(H-(v-mn)/(mx-mn||1)*H).toFixed(1)]);
  const d='M'+pts.map(p=>p.join(',')).join(' L'); const id='g'+Math.random().toString(36).slice(2);
  let extra=''; if(opts.data2){const v2=opts.data2;const p2=v2.map((v,i)=>[(i/(v2.length-1)*W2).toFixed(1),(H-((v??0)-mn)/(mx-mn||1)*H).toFixed(1)]);const d2='M'+p2.map(p=>p.join(',')).join(' L');
    extra=`<path d="${d2} L${W2},${H} L0,${H}Z" fill="${opts.color2}" opacity=".18"/><path d="${d2}" fill="none" stroke="${opts.color2}" stroke-width="1.4" vector-effect="non-scaling-stroke"/>`}
  return `<svg viewBox="0 0 ${W2} ${H}" preserveAspectRatio="none" style="height:${H}px"><defs><linearGradient id="${id}" x1="0" x2="0" y1="0" y2="1"><stop offset="0" stop-color="${color}" stop-opacity=".42"/><stop offset="1" stop-color="${color}" stop-opacity="0"/></linearGradient></defs><path d="${d} L${W2},${H} L0,${H}Z" fill="url(#${id})"/><path d="${d}" fill="none" stroke="${color}" stroke-width="1.6" vector-effect="non-scaling-stroke"/>${extra}</svg>`;
}
function ring(v,size,label){const r=36,c=2*Math.PI*r,pct=Math.max(0,Math.min(100,v||0));const id='rg'+Math.random().toString(36).slice(2);
  return `<div class="ring" style="width:${size||86}px;height:${size||86}px"><svg viewBox="0 0 84 84"><defs><linearGradient id="${id}"><stop offset="0" stop-color="#8b5cf6"/><stop offset="1" stop-color="#22d3ee"/></linearGradient></defs><circle cx="42" cy="42" r="${r}" fill="none" stroke="var(--line)" stroke-width="7"/><circle cx="42" cy="42" r="${r}" fill="none" stroke="${v>=50?`url(#${id})`:'var(--bad)'}" stroke-width="7" stroke-linecap="round" stroke-dasharray="${(c*pct/100).toFixed(1)} ${c.toFixed(1)}" style="transition:stroke-dasharray .8s"/></svg><b>${v==null?'—':v}</b>${label?`<small>${label}</small>`:''}</div>`}
function scoreHTML(s){if(!s||!s.n) return '<span class="n">—</span>';const v=s.score;return `<span class="sc" title="Сервисы ${Math.round(s.up*100)}% → ${s.pUp}/40\nОтклик ${s.ms||'—'} мс → ${s.pPing}/20\nСкорость ${s.ns?s.mbps.toFixed(1)+' Мбит/с':'—'} → ${s.pSpeed}/25\nБез заморозки ${s.ns?Math.round(s.dlOK*100)+'%':'—'} → ${s.pDL}/15${s.pJit?'\nРазброс отклика ±'+s.jitter+' мс → −'+s.pJit:''}${s.eligible?'':'\n(мало данных)'}"><s><u style="width:${v}%;background:${col(v)}"></u></s><span style="color:${col(v)}">${s.eligible?v:'…'+v}</span></span>`}
// плавная смена чисел
function countTo(el,to,fmt){const from=parseFloat(el.dataset.v||to);el.dataset.v=to;if(!fxOn()||from===to||isNaN(from)){el.innerHTML=fmt(to);return}const t0=performance.now();const step=t=>{const k=Math.min(1,(t-t0)/600),e=1-Math.pow(1-k,3);el.innerHTML=fmt(from+(to-from)*e);if(k<1)requestAnimationFrame(step)};requestAnimationFrame(step)}
// наклон карточек под мышкой (только полные эффекты)
document.addEventListener('mousemove',e=>{if(!fxOn())return;const c=e.target.closest('.tilt');document.querySelectorAll('.tilt').forEach(x=>{if(x!==c)x.style.transform=''});if(!c)return;const r=c.getBoundingClientRect();const x=(e.clientX-r.left)/r.width-.5,y=(e.clientY-r.top)/r.height-.5;c.style.transform=`perspective(900px) rotateX(${(-y*4).toFixed(2)}deg) rotateY(${(x*5).toFixed(2)}deg)`});

// ---------- навигация (2.3: 8 разделов, внутри — вкладки) ----------
// Ключ вкладки: «страница» или «страница:фильтр». Страница — это <section id="p-…">.
const SECS=[['home','◉','Обзор'],['watch','🛡','Сторож'],['servers','☰','Серверы'],
  ['devices','📱','Устройства',[['devices','Список'],['map','Карта']]],
  ['sites','🌐','Правила'],['net','📶','Сеть'],
  ['logs','🗒','Журнал',[['events','События'],['logs:access','Xray'],['logs:keenetic','Роутер'],['logs:selector','Программа']]],
  ['settings','⚙','Настройки',[['settings:main','Основные'],['settings:tg','Telegram'],['tools:sub','Подписка'],['tools:reserve','Резерв'],['tools:maint','Обслуживание'],['settings:pw','Пароль']]]];
const PAGES=SECS; // совместимость со старым кодом
const TABS=['home','watch','servers','devices','more'];
let pageKey=store.get('pageKey','home'), pageFilter='';
page=pageKey.split(':')[0]; pageFilter=pageKey.split(':')[1]||'';
function secOfKey(k){for(const s of SECS){if(s[0]===k&&!s[3])return s;if(s[3]&&s[3].some(x=>x[0]===k))return s}return null}
function resolveKey(p){
  if(p&&p[0]==='=')return p.slice(1); // точный ключ вкладки (кнопки вкладок): «devices» — это и раздел, и вкладка «Список»
  const sec=SECS.find(s=>s[0]===p);
  if(sec&&sec[3]&&curSec()[0]===p)return sec[3][0][0]; // повторный клик по разделу — на первую вкладку
  if(sec){if(!sec[3])return p;const last=store.get('sub_'+p,'');return sec[3].some(x=>x[0]===last)?last:sec[3][0][0]}
  for(const s of SECS){if(!s[3])continue;const x=s[3].find(x=>x[0]===p)||s[3].find(x=>x[0].split(':')[0]===p);if(x)return x[0]}
  return 'home';
}
function curSec(){return secOfKey(pageKey)||SECS[0]}
function renderNav(){
  const unread=unreadCount(), cs=curSec()[0];
  const bd=id=>id==='logs'&&unread?`<span class="badge">${unread}</span>`:'';
  $('#nav').innerHTML=SECS.map(([id,ic,t])=>`<a data-go="${id}" class="${cs===id?'on':''}"><span class="ic">${ic}</span>${t}${bd(id)}</a>`).join('');
  $('#tabbar').innerHTML=TABS.map(id=>{if(id==='more')return `<a data-go="more" class="${!TABS.includes(cs)?'on':''}"><span class="ic">⋯</span>Ещё${unread?`<span class="badge">${unread}</span>`:''}</a>`;const p=SECS.find(x=>x[0]===id);return `<a data-go="${id}" class="${cs===id?'on':''}"><span class="ic">${p[1]}</span>${p[2]}</a>`}).join('');
  $('#sheet').innerHTML=SECS.filter(p=>!TABS.includes(p[0])).map(([id,ic,t])=>`<a data-go="${id}"><span>${ic}</span>${t}${id==='logs'&&unread?` <span class="tag b">${unread}</span>`:''}</a>`).join('');
  const sec=curSec(); const st=$('#subtabs');
  st.innerHTML=sec[3]?`<div class="seg">${sec[3].map(([k,t])=>`<button data-go="=${k}" class="${pageKey===k?'on':''}">${t}${k==='events'&&unread?` <b class="bdg">${unread}</b>`:''}</button>`).join('')}</div>`:'';
  st.style.display=sec[3]?'':'none';
}
function go(p){
  if(p==='more'){$('#sheet').classList.add('on');$('#shade').classList.add('on');return}
  closeOverlays(); const key=resolveKey(p); const sec=secOfKey(key);
  const prevPage=page; pageKey=key; page=key.split(':')[0]; pageFilter=key.split(':')[1]||'';
  store.set('pageKey',key); if(sec&&sec[3]) store.set('sub_'+sec[0],key);
  document.querySelectorAll('.page').forEach(x=>x.classList.toggle('on',x.id==='p-'+page));
  $('#title').textContent=sec?sec[2]:'';
  if(page==='logs') syncLogTab();
  renderNav(); renderPage(); window.scrollTo(0,0); if(typeof renderSiteBar==='function')renderSiteBar();
  if(page==='logs') loadLogs(); if(page==='sites'){loadSites();loadRules()} if(page==='tools') loadTools(); if(page==='devices'){loadDevices();typeof loadRules==='function'&&loadRules()} if(page==='map') loadFlow(); if(page==='events') markRead();
  if(page==='net'){typeof loadDNS==='function'&&loadDNS();typeof loadPulse==='function'&&loadPulse()}
}
// показать только карточки текущей вкладки (data-st="…")
function filterCards(el){if(!el)return;el.querySelectorAll('[data-st]').forEach(c=>{c.style.display=(!pageFilter||c.dataset.st.split(' ').includes(pageFilter))?'':'none'})}
function closeOverlays(){$('#sheet').classList.remove('on');$('#drawer').classList.remove('on');$('#shade').classList.remove('on')}
$('#shade').onclick=closeOverlays;
document.addEventListener('keydown',e=>{if(e.key==='Escape')closeOverlays()});

// ---------- шапка ----------
// «Keenetic ↗» — веб-интерфейс роутера: тот же хост, порт из настроек роутера (RCI), по умолчанию 80.
// Если панель открыта по доменному имени (удалённо) — my.keenetic.net.
let ABOUT=null;
function routerURL(){const h=location.hostname;const ip=/^\d+\.\d+\.\d+\.\d+$/.test(h)||h==='localhost';
  if(!ip) return 'https://my.keenetic.net/';const p=ABOUT&&ABOUT.routerPort||80;return 'http://'+h+(p!==80?':'+p:'')+'/'}
async function loadAbout(){try{ABOUT=await api('/api/about')}catch(e){}$('#kneLink').href=routerURL();{const su=ABOUT&&ABOUT.supportURL;for(const id of ['#thxSide','#thxBtn']){const t=$(id);if(t){if(su){t.href=su;t.style.display=''}else t.style.display='none'}}}if(typeof demoBanner==='function')demoBanner()}
$('#kneLink').href=routerURL();
async function restartXray(){
  if(!confirm('Перезапустить XKeen и Xray? Соединения у всех устройств в политике xkeen прервутся на 5–10 секунд.'))return;
  toast('Перезапускаю Xray…');try{await api('/api/xray-restart',{});toast('Xray перезапущен');setTimeout(loadState,3500)}catch(e){toast('Ошибка: '+e.message)}
}
$('#restartBtn').onclick=restartXray;
$('#bellBtn').onclick=()=>go('events');
function unreadCount(){if(!W)return 0;return (W.events||[]).filter(e=>e.id>(W.lastReadEv||0)&&e.kind!=='info').length}
function renderBell(){const n=unreadCount();const b=$('#bellN');b.style.display=n?'':'none';b.textContent=n>99?'99+':n}
async function markRead(){if(!W||!W.events||!W.events.length)return;const last=W.events[W.events.length-1].id;if(last<=(W.lastReadEv||0))return;W.lastReadEv=last;renderBell();renderNav();try{await api('/api/events',{readUpTo:last})}catch(e){}}

// ---------- опрос ----------
let lastMetT=0;
async function loadState(){
  try{S=await api('/api/state');W=S.watch||{groups:{},health:{},events:[],settings:{}};}catch(e){$('#apiPill').innerHTML='<span class="dot bad"></span>панель недоступна';return}
  const ok=!S.apiError&&S.apiInConfig;
  $('#apiPill').innerHTML=`<span class="dot ${ok?'live':'bad'}"></span>${ok?'Xray онлайн · API':'Xray API недоступен'}`;
  $('#ver').textContent=S.version.replace(/\.0$/,'');
  renderBell(); renderNav(); renderPage();
}
async function loadMetrics(){
  try{const m=await api('/api/metrics?since='+lastMetT);if(!MET||!lastMetT){MET=m}else{MET.points=MET.points.concat(m.points).slice(-1800);Object.assign(MET,{...m,points:MET.points})}
    if(MET.points.length) lastMetT=MET.points[MET.points.length-1].t;
    if(page==='home') renderMetrics();}catch(e){}
}
async function loadTraffic(){try{TRF=await api('/api/traffic');if(page==='home'||page==='devices')renderPage()}catch(e){}}
async function loadFlow(){try{FLOW=await api('/api/flow');if(page==='map'||page==='home')drawFlowAll()}catch(e){}}
async function loadDevices(){try{DEVS=(await api('/api/devices')).devices||[];if(page==='devices')renderDevices()}catch(e){}}
function renderPage(){if(!S)return;({home:renderHome,watch:renderWatch,servers:renderServers,devices:renderDevices,map:renderMap,events:renderEvents,logs:renderLogsShell,sites:renderRulesPage,tools:renderTools,settings:renderSettings,net:renderNet}[page]||renderHome)()}

// активная группа сторожа и текущий сервер
function mainGroup(){return S&&S.groups&&S.groups[0]}
function wg(tag){return (W&&W.groups&&W.groups[tag])||{enabled:false,servers:[]}}
function health(tag){return W&&W.health&&W.health[tag]}
function curServer(){const g=mainGroup();if(!g)return'';return g.override||g.current||''}
function srvInfo(tag){return (S.servers||[]).find(s=>s.tag===tag)||{}}
</script>
