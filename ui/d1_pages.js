<script>
"use strict";
const ICON={switch:'🔄',warn:'⚠️',recover:'✅',restart:'♻️',auto:'🧪',critical:'🔴',restored:'🟢',help:'🆘',info:'ℹ️'};
const SVC_SHORT={'Instagram':'IG','IG видео':'IG▶','YouTube':'YT','YT видео':'YT▶','Telegram':'TG','TG медиа':'TG▶','WhatsApp':'WA','WA медиа':'WA▶'};

// ================= ОБЗОР =================
function renderHome(){
  const el=$('#p-home'); const g=mainGroup(); const cur=curServer(); const h=health(cur); const st=W.settings||{};
  const ok=!S.apiError&&S.apiInConfig; const wgp=g?wg(g.tag):{};
  let cls='',title='VPN работает нормально',sub='';
  if(!ok){cls='bad';title='Нет связи с Xray';sub=esc(S.apiError||'API не настроен')}
  else if(!cur){cls='warn';title='Нет живых серверов';sub='Xray использует запасной сервер'}
  else if(h&&h.t&&!h.ok){const anyOk=(wgp.servers||[]).some(s=>health(s)?.ok);cls=anyOk?'warn':'bad';title=anyOk?'Есть сбои — сторож разбирается':'VPN не работает';sub=esc(h.reason||'')}
  else sub=`Трафик идёт через ${srvName(cur)} · ${g.override?(wgp.enabled?'выбрал сторож':'выбран вручную'):'выбрал Xray (авто)'}${h&&h.okSince?' · без сбоев '+dur(Date.now()/1000-h.okSince):''}`;
  const today=TRF&&TRF.today; const vpnToday=today?sumObj(today.servers)||sumObj(today.devVPN):0;
  const isp=TRF&&TRF.isp||[]; const ispBad=isp.slice(-1440).filter(p=>!p.ok).length;
  const sw24=(W.events||[]).filter(e=>e.kind==='switch'&&e.t>Date.now()/1000-86400).length;
  // герой
  const info=srvInfo(cur); const sc=S.scores&&S.scores[cur]; const {flag,name}=splitFlag(cur);
  const proto=[info.protocol,info.network,info.security&&info.security!=='none'?info.security:''].filter(Boolean).join(' · ');
  const targets=(st.targets||[]);
  const svcCells=targets.filter((t,i)=>i<8).map(t=>{const r=h&&h.targets&&h.targets[t.name];return `<div><span>${esc(t.name)}</span><strong class="${!r?'n':r.ok?'g':'b'}">${!r?'…':r.ok?'✓ '+r.ms+' мс':'✕ '+esc((r.err||'').slice(0,18))}</strong></div>`}).join('');
  const spd=h&&h.speedT?(h.speedErr?`<span class="b">${/зависло/.test(h.speedErr)?'поток завис':'✕'}</span>`:mbps(h.speedKbps)):'—';
  const wHist=(S.scores&&cur)?null:null;
  el.innerHTML=`
  <div data-wg="status" class="status ${cls}"><span class="dot ${cls==='bad'?'bad':cls==='warn'?'mid':'live'}"></span><div><h2>${title}</h2><p>${sub}</p></div>
    <div class="r">${(()=>{const a=(TRF&&TRF.wans||[]).find(x=>x.active);return a?'провайдер '+esc(a.desc||a.id):'интернет провайдера'})()}: <span class="${ispBad?'m':'g'}">${isp.length?(ispBad?ispBad+' сбоев за сутки':'без сбоев'):'—'}</span><br>обрывов за сутки: ${typeof PULSE!=='undefined'&&PULSE?(()=>{const d=(PULSE.outages||[]).filter(o=>o.from>Date.now()/1000-86400);return d.length?`<span class="m" data-go="net" style="cursor:pointer">${d.length} (последний ${tm(d[d.length-1].from)})</span>`:'<span class="g">нет</span>'})():'—'}<br>DNS: ${typeof DNSR!=='undefined'&&DNSR&&DNSR.level?`<span class="${DNSR.level==='ok'?'g':DNSR.level==='warn'?'m':'b'}" data-go="net" style="cursor:pointer">${DNSR.level==='ok'?'в порядке':DNSR.level==='warn'?'часть серверов не отвечает':'не отвечает!'}</span>`:'—'}<br>через VPN сегодня: ${vpnToday?gb(vpnToday):'—'} · переключений: ${sw24}${subLine(st)}</div></div>
  <div data-wg="isp">${homeProviders()}</div>
  <div data-wg="hero" class="grid ghero">
    <div class="card glass tilt">
      <h3>Сейчас VPN идёт через<span class="r">${esc(g?g.tag:'')}</span></h3>
      ${cur?`<div class="hero"><div class="flag">${flag||'🌐'}</div><div><div class="nm">${esc(name.replace(/\(.*\)$/,''))} <span>${esc((name.match(/\((.*)\)/)||[])[1]||'')}</span></div>
        <div class="tags">${h&&h.ok?'<span class="tag g">● работает</span>':h&&h.t?'<span class="tag b">● сбоит</span>':'<span class="tag">не проверялся</span>'}${wgp.enabled?'<span class="tag a">🛡 сторож</span>':''}<span class="tag">${esc(proto)}</span></div></div>
        ${ring(sc&&sc.n?sc.score:null,86,'баллов')}</div>
      <div class="svc">${svcCells||'<div class="note">Добавьте серверы в сторож, чтобы видеть сервисы</div>'}</div>
      <div style="display:flex;justify-content:space-between;align-items:flex-end;margin-top:14px;gap:16px">
        <div><div class="note">Скорость через сервер</div><div class="mono" style="font-size:28px;font-weight:700">${spd}<small class="n" style="font-size:13px"> ${h&&h.speedT&&!h.speedErr?'Мбит/с':''}</small></div></div>
        <div style="flex:1;max-width:360px;height:54px" id="heroSpark"></div>
        <button class="ib" data-info="${esc(cur)}" title="Подробнее">i</button></div>`
      :'<div class="empty">Сервер не выбран</div>'}
    </div>
    <div class="grid g2" id="metCards"></div>
  </div>
  <div data-wg="watch">${homeWatchCard()}</div>
  <div data-wg="traffic" class="grid g2">
    <div class="card"><h3>Трафик через VPN · сегодня<span class="r mono">${vpnToday?gb(vpnToday):''}</span></h3>${trafficBlock()}</div>
    <div class="card"><h3>Последние события<span class="r lnk" data-go="events">все →</span></h3>${eventsList((W.events||[]).filter(e=>e.kind!=='info').slice(-7).reverse(),true)||'<div class="empty">Событий пока нет</div>'}</div>
  </div>`;
  renderMetrics(); heroSpark(); if(typeof applyWidgets==='function')applyWidgets(el);
}
function subLine(st){const e=st.subExpire||st.subInfExpire||st.subPaidUntil||0,tot=st.subTotal||0;if(!e&&!tot)return '';const now=Date.now()/1000,used=(st.subUp||0)+(st.subDown||0);
  const d=e?(e-now)/86400:null;const cls=e&&d<3?'b':tot&&used>tot*.8?'m':'g';
  return `<br>подписка: <span class="${cls}" data-go="tools:sub" style="cursor:pointer">${e?(d>0?'ещё '+(d<1?Math.round(d*24)+' ч':Math.round(d)+' дн'):'истекла'):''}${e&&tot?' · ':''}${tot?gb(used)+' из '+gb(tot):''}</span>`}
function sumObj(o){let s=0;for(const k in o||{}){s+=(o[k].up||0)+(o[k].down||0)}return s}
function ispBar(isp){if(!isp.length)return '<i class="e"></i>'.repeat(96);const now=Date.now()/1000;let out='';for(let i=95;i>=0;i--){const a=now-(i+1)*900,b=now-i*900;const pts=isp.filter(p=>p.t>=a&&p.t<b);out+=!pts.length?'<i class="e"></i>':pts.some(p=>!p.ok)?(pts.every(p=>!p.ok)?'<i class="x"></i>':'<i class="y"></i>'):'<i></i>'}return out}
function heroSpark(){const el=$('#heroSpark');if(!el)return;const cur=curServer();const pts=(MET&&MET.points)||[];
  // отклик активного сервера нет в метриках — рисуем входящую скорость интернета как «пульс»
  el.innerHTML=area(pts.slice(-150).map(p=>p.rx),'#22d3ee',54)}
function renderMetrics(){
  const box=$('#metCards'); if(!box) return; const m=MET; const P=(m&&m.points)||[]; const last=P[P.length-1]||{};
  const memMB=last.mem?Math.round(last.mem/1024):0, totMB=m&&m.memTotal?Math.round(m.memTotal/1024):0;
  const cards=[
    ['Процессор',m&&m.cores?m.cores+' ядра':'',`${last.cpu??'—'}<small>%</small>`,m&&m.load?'нагрузка '+m.load:'',P.map(p=>p.cpu),'#a78bfa'],
    ['Память',totMB?totMB+' МБ':'',`${memMB||'—'}<small>МБ · ${totMB?Math.round(memMB*100/totMB):'—'}%</small>`,last.xrayM?'Xray: '+Math.round(last.xrayM/1024)+' МБ':'',P.map(p=>p.mem),'#22d3ee'],
    ['Интернет'+(m&&m.iface?' · '+m.iface:''),`↑ ${last.tx??'—'}`,`↓ ${last.rx??'—'}<small>Мбит/с</small>`,'вход и выход за час',P.map(p=>p.rx),'#34d399',P.map(p=>p.tx)],
    m&&m.hasTemp?['Температура','',`${last.temp?last.temp.toFixed(0):'—'}<small>°C</small>`,'датчик роутера',P.map(p=>p.temp),'#fbbf24']
      :['Xray (память)','',`${last.xrayM?Math.round(last.xrayM/1024):'—'}<small>МБ</small>`,'датчика температуры нет',P.map(p=>p.xrayM),'#fbbf24'],
  ];
  box.innerHTML=cards.map(([t,r,v,sub,d,c,d2])=>`<div class="card met tilt"><div class="lab"><span>${t}</span><span>${r}</span></div><div class="val">${v}</div><div class="sub">${sub}</div>${area(d.slice(-600),c,42,d2?{data2:d2.slice(-600),color2:'#a78bfa'}:{})}</div>`).join('');
  if(m&&m.uptime) $('#sidefoot').innerHTML=`Роутер работает ${dur(m.uptime)}<br>${esc(S?'xray-selector '+S.version:'')}`;
}
function homeWatchCard(){
  const g=mainGroup(); if(!g) return ''; const w=wg(g.tag);
  if(!w.servers.length) return `<div class="card"><h3>🛡 Сторож</h3><div class="note">Сторож не настроен. <span class="lnk" data-go="watch">Добавить серверы →</span></div></div>`;
  return `<div class="card"><h3>🛡 Сторож · ${esc(g.tag)}<span class="r">${w.enabled?'проверка каждые '+(W.settings.intervalSec||15)+' с · '+ago(W.lastRound):'выключен'} · <span class="lnk" data-go="watch">управление →</span></span></h3>${watchTable(g,true)}</div>`;
}
function trafficBlock(){
  if(!TRF) return '<div class="empty"><span class="spin"></span></div>';
  const t=TRF.today||{}; const names=(S&&S.devNames)||{};
  const top=(o,n,lab)=>Object.entries(o||{}).map(([k,v])=>[k,(v.up||0)+(v.down||0),v.conns||0]).filter(x=>x[1]||x[2]).sort((a,b)=>(b[1]-a[1])||(b[2]-a[2])).slice(0,n);
  const svc=top(t.services,10); const mx=Math.max(1,...svc.map(x=>x[1]||x[2])); const totB=Object.values(t.services||{}).reduce((a,v)=>a+(v.up||0)+(v.down||0),0)||1;
  let warn=''; if(!TRF.statsConfigured) warn=`<div class="banner" style="margin-bottom:10px">Точный трафик по серверам выключен в Xray. <span class="lnk" data-go="settings">Включить в настройках →</span></div>`;
  if(TRF.ctErr) warn+=`<div class="note" style="margin-bottom:8px">По устройствам: ${esc(TRF.ctErr)}</div>`;
  return warn+`<div>${svc.map(([k,b,c])=>{const ip=k==='по IP-адресу';return `<div style="display:grid;grid-template-columns:minmax(110px,170px) 1fr 74px;gap:10px;align-items:center;padding:6px 0;border-top:1px solid var(--line)"><div style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap"><b class="${ip?'n':''}">${esc(ip?'прочие (только IP)':k)}</b><div class="note">${c} соед.</div></div><div class="prog"><i style="width:${Math.round((b||c)*100/mx)}%"></i></div><div class="mono" style="text-align:right">${b?gb(b):'—'}<div class="note">${b?Math.round(b*100/totB)+'%':''}</div></div></div>`}).join('')||'<div class="note">пока нет данных</div>'}</div><div class="note" style="margin-top:8px">Сервис определяется по имени сайта, а приложения, которые ходят сразу по IP (Telegram, звонки, игры), — по известным диапазонам и по спискам из раздела «Правила».</div>`;
}
function eventsList(list,compact){return list.map(e=>`<div class="ev ${e.id>(W.lastReadEv||0)&&e.kind!=='info'?'unread':''}"><span class="tm">${tm(e.t)}</span><span>${ICON[e.kind]||'•'}</span><span>${e.group?`<b>${esc(e.group)}</b>: `:''}${esc(e.text)}</span></div>`).join('')}

// ================= СТОРОЖ =================
// свёрнутые уровни сторожа: отдельно для Обзора (h) и страницы Сторожа (w), хранится на устройстве
function tierCol(compact){try{return new Set(JSON.parse(store.get('tierCol_'+(compact?'h':'w'),'[]')))}catch(e){return new Set()}}
document.addEventListener('click',e=>{const t=e.target.closest('.tsec');if(!t)return;const k='tierCol_'+t.dataset.tctx;let l=[];try{l=JSON.parse(store.get(k,'[]'))}catch(_){}
  const x=t.dataset.tier;l=l.includes(x)?l.filter(y=>y!==x):[...l,x];store.set(k,JSON.stringify(l));renderPage()});
function watchTable(g,compact){
  const w=wg(g.tag), st=W.settings||{}, pins=new Set(w.pinned||[]), resv=new Set(w.reserve||[]), locks=new Set(w.locked||[]), cur=g.override||'';
  const nStable=w.auto?w.servers.filter(s=>pins.has(s)).length:0, nRes=w.auto?w.servers.filter(s=>resv.has(s)).length:0;
  const tg=(st.targets||[]).slice(0,8);
  let rows='';
  w.servers.forEach((t,i)=>{
    const tier=!w.auto?'':i<nStable?'main':i<nStable+nRes?'res':'cand';const tc=tierCol(compact);
    const sh=(k,txt)=>`<tr class="sec tsec" data-tier="${k}" data-tctx="${compact?'h':'w'}" title="Нажмите, чтобы ${tc.has(k)?'развернуть':'свернуть'}"><td colspan="7"><span class="tarr">${tc.has(k)?'▸':'▾'}</span> ${txt}${tc.has(k)?' <span class="n">— свёрнуто</span>':''}</td></tr>`;
    if(w.auto&&i===0&&nStable) rows+=sh('main',`📌 Основные (${nStable}/${st.mainSlots||6}) — сторож выбирает из них по баллам`);
    if(w.auto&&i===nStable&&nRes) rows+=sh('res',`🟡 Запасные (${nRes}/${st.reserveSlots??3}) — прошли проверку; лучший заменит слабый основной (не чаще раза в 6 ч)`);
    if(w.auto&&i===nStable+nRes) rows+=sh('cand',`🧪 На проверке (${w.servers.length-nStable-nRes}/${st.candSlots??5}) — плохие меняются через 30 мин, прошедшие — в запасные`);
    if(tier&&tc.has(tier)) return;
    const h=health(t), sc=S.scores&&S.scores[t], act=cur===t;
    const chips=tg.map(x=>{const r=h&&h.targets&&h.targets[x.name];const s=SVC_SHORT[x.name]||x.name;return !r?`<span class="ck w">${esc(s)}</span>`:r.ok?`<span class="ck" title="${esc(x.name)}: ${r.ms} мс">${esc(s)} ${compact?'✓':r.ms}</span>`:`<span class="ck x" title="${esc(x.name)}: ${esc(r.err||'')}">${esc(s)} ✕</span>`}).join('');
    let state=!h||!h.t?'<span class="n">ещё не проверялся</span>':h.ok?`<span class="g">● здоров</span>${h.okSince?' <span class="n">'+dur(Date.now()/1000-h.okSince)+'</span>':''}`:`<span class="b">● сбоев подряд: ${h.consecFail}</span>`;
    if(w.auto&&!pins.has(t)){const t0=(w.trials||{})[t];if((w.passed||{})[t])state+=' · <span class="g">✓ прошёл проверку</span>';else if(t0){const m=Math.round((Date.now()/1000-t0)/60),tot=st.trialMin||120;state+=` · <span class="n">проверка ${m}/${tot} мин</span><div class="prog" style="margin-top:4px;max-width:180px"><i style="width:${Math.min(100,m*100/tot)}%"></i></div>`}}
    const spd=!h||!h.speedT?'<span class="n">—</span>':h.speedErr?`<span class="b">${/зависло/.test(h.speedErr)?'завис':'✕'}</span>`:`<span class="${h.speedKbps<(st.speedMinKbps||1500)?'b':h.speedKbps<(st.speedMinKbps||1500)*2?'m':'g'}">${mbps(h.speedKbps)}</span>`;
    const actions=compact?'':`<div class="mini"><button data-wmove="${i}" data-dir="-1" data-wg="${esc(g.tag)}" ${i===0?'disabled':''} title="Выше">↑</button><button data-wmove="${i}" data-dir="1" data-wg="${esc(g.tag)}" ${i===w.servers.length-1?'disabled':''} title="Ниже">↓</button>${w.auto?`<button data-wlock="${esc(t)}" data-wg="${esc(g.tag)}" title="${locks.has(t)?'Снять замок':'Замок: автоматика не понижает и не убирает'}" style="${locks.has(t)?'':'opacity:.35'}">🔒</button><button data-wpin="${esc(t)}" data-wg="${esc(g.tag)}" title="${pins.has(t)?'Убрать из основных':'В основные'}" style="${pins.has(t)?'color:#eab308':''}">📌</button>`:''}<button data-wdel="${esc(t)}" data-wg="${esc(g.tag)}" title="Убрать">✕</button></div>`;
    rows+=`<tr class="${act?'act':''}"><td class="c-n n mono">${i+1}</td><td class="c-name"><div style="font-weight:600;white-space:nowrap">${srvName(t,true)} ${act?'<span class="tag a">активный</span>':''} ${w.best===t?'<span class="tag" title="Больше всего баллов за последний час среди здоровых — сторож держится его и подключается к нему сразу после запуска или перезапуска Xray">⭐ лучший</span>':''}</div><div style="font-size:12px">${state}</div></td><td class="c-chk"><div class="chk">${chips}</div></td><td class="c-spd mono">${spd}<div class="n" style="font-size:11px">Мбит/с</div></td><td class="c-sc">${scoreHTML(sc)}</td><td class="c-act">${actions}</td><td class="c-i"><span class="ib" data-info="${esc(t)}">i</span></td></tr>`;
  });
  return `<table class="t wt"><thead><tr><th>#</th><th>Сервер</th><th>Сервисы</th><th>Скорость</th><th>Баллы</th><th></th><th></th></tr></thead><tbody>${rows}</tbody></table>`;
}
function renderWatch(){
  const el=$('#p-watch'); if(!S.groups.length){el.innerHTML='<div class="card empty">В конфиге Xray нет групп (balancer)</div>';return}
  if(el.contains(document.activeElement)&&document.activeElement.tagName==='SELECT') return;
  const st=W.settings||{};
  el.innerHTML=S.groups.map(g=>{const w=wg(g.tag);const b=S.balSync&&S.balSync[g.tag];
    const opts=S.servers.filter(s=>!w.servers.includes(s.tag)&&!(S.dead||[]).includes(s.tag)).map(s=>`<option value="${esc(s.tag)}">${esc(s.tag)}</option>`).join('');
    const evs=(W.events||[]).filter(e=>!e.group||e.group===g.tag).slice(-10).reverse();
    let sync='';
    if(b&&!b.inSync) sync=`<div class="banner">Группа <b>${esc(g.tag)}</b> в ${esc(b.file)} отличается от списка сторожа. <button class="btn sm" data-bsync="${esc(g.tag)}">Синхронизировать</button></div>`;
    else if(b&&b.pending) sync=`<div class="banner">Состав группы обновлён в ${esc(b.file)}, Xray применит его после перезапуска. <button class="btn sm pri" data-xrestart="1">Применить сейчас</button></div>`;
    else if(b) sync=`<div class="note">✓ Группа <b>${esc(g.tag)}</b> в конфиге Xray совпадает со ${w.auto?'стабильными серверами':'списком'} — если сторож выключится, Xray выберет из них.</div>`;
    return `<div class="card">
      <h3>🛡 Сторож · ${esc(g.tag)}<span class="r">${w.enabled?(W.running?'<span class="spin"></span> проверяю…':'каждые '+(st.intervalSec||15)+' с · '+ago(W.lastRound)):''}</span></h3>
      <div class="tools" style="margin-bottom:12px">
        <label class="sw"><input type="checkbox" data-wtoggle="${esc(g.tag)}" ${w.enabled?'checked':''}><span class="k"></span>${w.enabled?'Включён':'Выключен'}</label>
        <label class="sw x-exp"><input type="checkbox" data-wauto="${esc(g.tag)}" ${w.auto?'checked':''}><span class="k"></span>Автокандидаты</label>
        ${W.realtime?`<span class="tag m" title="Сервер не меняется из-за баллов, пока идёт игра или звонок. При сбое — переключит сразу.">🎮 игра/звонок — смена отложена</span>`:''}
        <span class="note">${w.auto?`${st.candSlots??5} мест под кандидатов · проверка ${st.trialMin||120} мин · проходной балл ${st.passScore||70}${(w.excluded||[]).length?` · <span class="lnk" data-wresetex="${esc(g.tag)}">вернуть исключённые (${w.excluded.length})</span>`:''}`:'список ведёте вы'}</span>
        <span class="sp"></span><button class="btn" data-wcheck="${esc(g.tag)}">⚡ Проверить сейчас</button></div>
      ${w.servers.length?watchTable(g,false):'<div class="empty">Добавьте 3–5 надёжных серверов: сторож будет часто проверять на них Instagram, YouTube, Telegram, WhatsApp и скорость и сам переключит, если активный начнёт сбоить.</div>'}
      <div class="tools" style="margin-top:12px"><button class="btn pri" data-wpick="${esc(g.tag)}">＋ Добавить серверы</button><span class="note">выбор нескольких сразу, с баллами и пингом</span></div>
      <div style="margin-top:12px">${sync}</div>
    </div>
    ${typeof tunnelCard==='function'?tunnelCard():''}
    <div class="card"><h3>События сторожа<span class="r lnk" data-go="events">все →</span></h3>${eventsList(evs)||'<div class="empty">Событий пока нет</div>'}</div>`}).join('');
}
async function wpost(body){try{await api('/api/watch',body);await loadState();return true}catch(e){toast('Ошибка: '+e.message);return false}}

// ================= ПАНЕЛЬ «i» =================
let drawerTag='';
async function openServer(tag){
  drawerTag=tag; const {flag,name}=splitFlag(tag);
  $('#dHead').innerHTML=`<div style="font-size:36px;line-height:1">${flag||'🌐'}</div><div style="flex:1;min-width:0"><div style="font-size:19px;font-weight:700">${esc(name)}</div><div class="note" id="dSub">загрузка…</div></div><button class="btn icon" onclick="closeOverlays()">✕</button>`;
  $('#dBody').innerHTML='<div class="empty"><span class="spin"></span></div>';
  $('#drawer').classList.add('on');$('#shade').classList.add('on');
  try{const d=await api('/api/server?tag='+encodeURIComponent(tag));if(drawerTag===tag)renderDrawer(d)}catch(e){$('#dBody').innerHTML='<div class="empty">'+esc(e.message)+'</div>'}
}
// п. 29: статистика из уже собранных проверок
function weekCard(d){const w=d.week;if(!w)return '';const bh=w.byHour||[];const any=bh.some(x=>x.days);
  const hrs=s=>s>=3600?(s/3600).toFixed(s>=36000?0:1)+' ч':Math.round(s/60)+' мин';
  return `<div class="card" style="padding:12px 14px"><h3>Надёжность ${qh('Считается из уже сделанных проверок сторожа, без новых запросов. Подробно — за сутки, дальше почасовые итоги до 7 дней.')}<span class="r">${w.days?'данные за '+w.days+' дн':''}</span></h3>
   <div class="kv"><div><span>Разброс отклика (6 ч)</span><b class="mono ${w.jitter6>300?'b':w.jitter6>120?'m':''}">${w.jitterN>=5?'±'+w.jitter6+' мс':'—'}</b></div>
   <div><span>Падений</span><b class="mono">${w.falls24} за сутки · ${w.falls7} за неделю</b></div>
   <div><span>Восстанавливается в среднем</span><b class="mono">${w.recAvg7?hrs(w.recAvg7):'—'}</b></div>
   <div><span>Был основным</span><b class="mono">${hrs(w.active24||0)} за сутки · ${hrs(w.active7||0)} за неделю</b></div>
   <div><span>Трафик за неделю</span><b class="mono">${d.traffic7?gb(d.traffic7):'—'}</b></div></div>
   ${any?`<div class="note" style="margin:10px 0 4px">Доступность по часам суток${w.hourPen?` · <span class="b">сейчас обычно сбоит — сторож снимает ${w.hourPen} баллов</span>`:''}</div>
   <div class="hbars">${bh.map(x=>`<i title="${x.h}:00 — ${x.days?x.up+'% ('+x.days+' дн)':'нет данных'}" style="height:${x.days?Math.max(8,x.up):4}%;background:${!x.days?'var(--line)':x.up>=95?'var(--good)':x.up>=80?'var(--mid)':'var(--bad)'}"></i>`).join('')}</div>
   <div class="note" style="display:flex;justify-content:space-between"><span>0</span><span>6</span><span>12</span><span>18</span><span>23</span></div>`:''}</div>`}
function renderDrawer(d){
  const s=d.server,sc=d.score||{},h=d.health,g=mainGroup(),gt=g?g.tag:'';
  const roles={stable:'<span class="tag a">📌 основной</span>',reserve:'<span class="tag">🟡 запасной</span>',candidate:'<span class="tag m">🧪 кандидат</span>',watch:'<span class="tag a">🛡 в стороже</span>'};
  $('#dSub').innerHTML=`${esc([s.protocol,s.network,s.security].filter(Boolean).join(' · '))} · <span class="mono">${esc(s.address)}:${s.port}</span><div class="tags">${d.active?'<span class="tag g">● активный</span>':''}${roles[d.role]||''}${d.dead?'<span class="tag b">💤 мёртвый</span>':''}</div>`;
  const B=(d.watch||[]).sort((a,b)=>a.t-b.t);
  const mon=d.monitor||[];
  const upPct=sc.n?Math.round(sc.up*100):(mon.length?Math.round(mon.filter(x=>x.ok).length*100/mon.length):null);
  const chart=B.length>1?area(B.map(b=>b.ok?b.ms:0),'#a78bfa',80):mon.length>1?area(mon.map(x=>x.ok?x.d:0),'#a78bfa',80):'<div class="note">Графика пока нет: сервер не проверялся сторожем</div>';
  const bars=B.length?B.slice(-96).map(b=>`<i class="${b.ok===b.n?'':b.ok===0?'x':'y'}"></i>`).join(''):mon.slice(-96).map(x=>`<i class="${x.ok?'':'x'}"></i>`).join('');
  const names=d.names||{};
  const devs=(d.devices||[]).sort((a,b)=>(b.bytes-a.bytes)||(b.conns-a.conns)).slice(0,8);
  const lines=(d.log||[]).slice(-40).map(l=>{let x=esc(l);for(const ip in names){x=x.split(esc(ip)+':').join(`<span class="dv">${esc(names[ip])}</span>:`)}return x.replace(/^(\S+ \S+)/,'<span class="ld">$1</span>')}).join('\n');
  const isW=d.role!=='';
  $('#dBody').innerHTML=`
   <div style="display:flex;gap:18px;align-items:center">${ring(sc.n?sc.score:null,78,'баллов')}<div class="kv" style="flex:1">
     <div><span>Доступность</span><b class="${upPct==null?'':upPct>=95?'g':upPct>=70?'m':'b'}">${upPct==null?'—':upPct+'%'}</b></div>
     <div><span>Отклик сервисов</span><b>${sc.ms?sc.ms+' мс':'—'}</b></div>
     <div><span>Скорость (медиана)</span><b>${sc.ns?sc.mbps.toFixed(1)+' Мбит/с':h&&h.speedT&&!h.speedErr?mbps(h.speedKbps)+' Мбит/с':'—'}</b></div>
     <div><span>Трафик сегодня</span><b>${d.traffic&&(d.traffic.up+d.traffic.down)?gb(d.traffic.up+d.traffic.down):(d.traffic&&d.traffic.conns?d.traffic.conns+' соед.':'—')}</b></div></div></div>
   ${(()=>{const a=d.score1h||{},b=d.score24h||{};if(!a.n&&!b.n)return '';const c=(x,f)=>x&&x.n?f(x):'—';
     return `<div class="card" style="padding:12px 14px"><h3>Статистика</h3><table class="t"><tr><th></th><th>за 1 ч</th><th>за 24 ч</th></tr>
      <tr><td>Доступность</td><td class="mono">${c(a,x=>Math.round(x.up*100)+'%')}</td><td class="mono">${c(b,x=>Math.round(x.up*100)+'%')}</td></tr>
      <tr><td>Отклик сервисов</td><td class="mono">${c(a,x=>x.ms?x.ms+' мс':'—')}</td><td class="mono">${c(b,x=>x.ms?x.ms+' мс':'—')}</td></tr>
      <tr><td>Скорость (медиана)</td><td class="mono">${c(a,x=>x.ns?x.mbps.toFixed(1)+' Мбит/с':'—')}</td><td class="mono">${c(b,x=>x.ns?x.mbps.toFixed(1)+' Мбит/с':'—')}</td></tr>
      <tr><td>Баллы</td><td class="mono">${c(a,x=>x.score)}</td><td class="mono">${c(b,x=>x.score)}</td></tr>
      <tr><td>Проверок</td><td class="mono n">${a.n||0}</td><td class="mono n">${b.n||0}</td></tr></table>
      <div class="note" style="margin-top:6px">Баллы на карточках и решения сторожа — за 6 ч.</div></div>`})()}
   <div class="card" style="padding:12px 14px"><h3>Отклик и доступность · 24 часа</h3>${chart}<div class="bar" style="margin-top:8px">${bars}</div></div>
   ${sc.n?`<div class="card" style="padding:12px 14px"><h3>Из чего баллы · за ${winTxt()}</h3><div class="row"><span>Сервисы открывались (${Math.round(sc.up*100)}%)</span><b class="mono">${sc.pUp}/40</b></div><div class="row"><span>Отклик (${sc.ms||'—'} мс)</span><b class="mono">${sc.pPing}/20</b></div><div class="row"><span>Скорость (${sc.ns?sc.mbps.toFixed(1)+' Мбит/с':'—'})</span><b class="mono">${sc.pSpeed}/25</b></div><div class="row"><span>Без заморозки (${sc.ns?Math.round(sc.dlOK*100)+'%':'—'})</span><b class="mono">${sc.pDL}/15</b></div>${sc.pJit?`<div class="row"><span>Разброс отклика (±${sc.jitter} мс)</span><b class="mono b">−${sc.pJit}</b></div>`:''}</div>`:''}
   ${weekCard(d)}
   <div class="card" style="padding:12px 14px"><h3>Кто пользуется · сегодня</h3>${devs.map(x=>`<div class="row"><span>${esc(x.name||x.id)}</span><span class="n mono">${x.bytes?gb(x.bytes)+' · ':''}${x.conns} соед.</span></div>`).join('')||'<div class="note">Пока никто</div>'}</div>
   <div class="card x-exp" style="padding:12px 14px"><h3>Журнал · только этот сервер</h3><div class="log" style="max-height:260px">${lines||'<span class="ld">записей нет</span>'}</div></div>
   <div class="tools">
     ${gt&&!d.active?`<button class="btn pri" data-pick="${esc(s.tag)}" data-group="${esc(gt)}">Сделать основным</button>`:''}
     <button class="btn" data-test1="${esc(s.tag)}">⚡ Проверить</button>
     ${gt?(isW?`<button class="btn" data-wdel="${esc(s.tag)}" data-wg="${esc(gt)}">Убрать из сторожа</button>`:`<button class="btn" data-wadd1="${esc(s.tag)}" data-wg="${esc(gt)}">🛡 В сторож</button>`):''}
     ${d.dead?`<button class="btn" data-undead="${esc(s.tag)}">↩ Вернуть в рабочие</button>`:(!isW&&!d.active?`<button class="btn" data-dead="${esc(s.tag)}">💤 В мёртвые</button>`:'')}
   </div>`;
}
</script>

<script>
function homeProviders(){const w=TRF&&TRF.wans||[];const by=TRF&&TRF.ispBy||{};
  if(!w.length){const isp=TRF&&TRF.isp||[];return `<div class="card"><h3>Интернет провайдера · 24 часа<span class="r">${isp.length&&isp[isp.length-1].ok?'пинг сейчас '+isp[isp.length-1].ms+' мс':''}</span></h3><div class="bar" style="height:12px">${ispBar(isp)}</div></div>`}
  return `<div class="card"><h3>Подключения<span class="r">провайдеры — связь с Яндексом раз в минуту · VPN — проверка сервисов</span></h3><div class="note" style="margin:2px 0 4px;font-weight:600">Провайдеры · 24 часа</div>${w.map(x=>{const l=by[x.name]||[],last=l[l.length-1],bad=l.filter(p=>!p.ok).length;
    return `<div style="display:grid;grid-template-columns:minmax(150px,220px) 1fr auto;gap:12px;align-items:center;margin:8px 0"><div><b>${esc(x.desc||x.id)}</b> <span class="tag ${x.role==='основной'?'a':''}">${esc(x.role||'')}</span>${x.active?' <span class="tag g">● активен</span>':''}<div class="note">${esc(x.id)} · ${esc(x.name)}${x.ip?' · '+esc(x.ip):''} · приоритет ${x.prio}</div></div>
      <div class="bar" style="height:12px">${ispBar(l)}</div><div class="mono ${!last?'n':last.ok?'g':'b'}" style="white-space:nowrap">${!last?'—':last.ok?last.ms+' мс':'нет связи'}${bad?` <span class="m">· ${bad} сбоев</span>`:''}</div></div>`}).join('')}${connVPN()}</div>`}
// VPN и прокси Keenetic (WireGuard, Proxy…) — внутри блока «Подключения»
function connVPN(){const l=(typeof TUN!=='undefined'&&TUN&&TUN.tunnels)||[];
  if(!l.length)return `<div class="note" style="margin-top:10px">VPN и прокси Keenetic не проверяются. Отметьте их в «Настройки → Резерв», чтобы видеть здесь, работают ли они.</div>`;
  return `<div class="note" style="margin:12px 0 4px;font-weight:600">VPN и прокси Keenetic</div>${l.map(r=>{const x=r.iface,h=r.health;
    const okN=h&&h.targets?Object.values(h.targets).filter(t=>t.ok).length:0,all=h&&h.targets?Object.keys(h.targets).length:0;
    return `<div style="display:grid;grid-template-columns:minmax(150px,220px) 1fr auto;gap:12px;align-items:center;margin:8px 0"><div><b>${esc(x.desc||x.id||x.name)}</b>${r.inReserve?' <span class="tag">в резерве</span>':''}${TUN.active===x.tag?' <span class="tag g">● сейчас через него</span>':''}<div class="note">${esc(x.type)} · ${esc(x.id||x.name)}</div></div>
      <div class="note">${!h||!h.t?'ещё не проверялось':h.ok?`<span class="g">✓ работает</span> · сервисы ${okN}/${all}${h.speedKbps?' · '+mbps(h.speedKbps)+' Мбит/с':''}`:`<span class="b">✕ ${esc(h.reason||'не открывает сервисы')}</span>`}</div>
      <div class="mono ${!h||!h.t?'n':h.ok?'g':'b'}" style="white-space:nowrap" title="отклик сервисов через это подключение (медиана)">${(()=>{const ms=h&&h.targets?Object.values(h.targets).filter(t=>t.ok&&t.ms).map(t=>t.ms).sort((a,b)=>a-b):[];return ms.length?ms[ms.length>>1]+' мс':'—'})()}</div></div>`}).join('')}`}
function homeTunnels(){if(typeof TUN==='undefined'||!TUN||!(TUN.tunnels||[]).length)return '';
  return `<div class="card"><h3>🛟 Резервные подключения Keenetic<span class="r lnk" data-go="watch">подробнее →</span></h3><div class="grid g3">${TUN.tunnels.map(r=>{const x=r.iface,h=r.health,sc=r.score;
    const okN=h&&h.targets?Object.values(h.targets).filter(t=>t.ok).length:0,all=h&&h.targets?Object.keys(h.targets).length:0;
    return `<div style="display:flex;gap:10px;align-items:center;padding:6px 0"><div style="flex:1;min-width:0"><b>${esc(x.type)} · ${esc(x.id||x.name)}</b>${r.inReserve?' <span class="tag g">в резерве</span>':''}${TUN.active===x.tag?' <span class="tag a">сейчас</span>':''}
      <div class="note">${x.desc?esc(x.desc)+' · ':''}${!h||!h.t?'ещё не проверялось':h.ok?`<span class="g">здорово</span> · сервисы ${okN}/${all}${h.speedKbps?' · '+mbps(h.speedKbps)+' Мбит/с':''}`:`<span class="b">${h.reason&&h.reason.includes('не активно')?'не подключено':'сбоит'}</span> · сервисы ${okN}/${all}`}</div></div>${scoreHTML(sc)}</div>`}).join('')}</div></div>`}
</script>