<script>
"use strict";
// ================= СЕРВЕРЫ =================
let srvTab=store.get('srvTab','active')==='dead'?'dead':'active', country='', srvQ='', srvSort=store.get('sort2','score');
function upOf(tag){const st=S.stats&&S.stats[tag];if(!st||!st.w)return null;const x=st.w[win];if(!x||!x.n)return {n:0,fail:0,pct:0,enough:false,bar:st.bar||''};
  const expected=Math.max(1,Math.floor(Math.min(winSec(),S.keepSec)/Math.max(1,S.monitorSec)));return {n:x.n,fail:x.fail,avg:x.avg,pct:Math.round(x.fail*100/x.n),enough:x.n>=Math.max(3,Math.floor(expected*.5)),bar:st.bar||''}}
function delayOf(tag){const r=S.results[tag],o=S.observatory[tag];if(S.testing.includes(tag))return{h:'<span class="spin"></span>',v:1e9-1};
  const oT=o?(o.lastTry||0)*1000:0;if(r&&(!o||r.t>=oT)){if(r.err)return{h:`<span class="b" title="${esc(r.err)}">✕</span>`,v:1e9};return{h:`<span class="${clsMs(r.delay)}">${r.delay} мс</span>`,v:r.delay}}
  if(o){if(!o.alive)return{h:'<span class="b">✕</span>',v:1e9};if(o.delay>0)return{h:`<span class="${clsMs(o.delay)}" style="opacity:.75">${o.delay} мс</span>`,v:o.delay}}return{h:'<span class="n">—</span>',v:1e9+1}}
function barHTML(bar){const pad=Math.max(0,30-bar.length);return '<i class="e"></i>'.repeat(pad)+[...bar].map(c=>c==='1'?'<i></i>':'<i class="x"></i>').join('')}
function shownTags(){const dead=new Set(S.dead||[]),q=srvQ.toLowerCase();return S.servers.filter(s=>{if((srvTab==='dead')!==dead.has(s.tag))return false;const c=splitFlag(s.tag).cc||'—';if(country&&c!==country)return false;return !q||(s.tag+' '+s.protocol+' '+s.network+' '+s.address+' '+c).toLowerCase().includes(q)}).map(s=>s.tag)}
function sortTags(t){const a=[...t];const sv=x=>{const s=S.scores&&S.scores[x];return s&&s.n?(s.eligible?s.score:s.score/1000):-1};
  if(srvSort==='score')a.sort((x,y)=>sv(y)-sv(x)||delayOf(x).v-delayOf(y).v);else if(srvSort==='delay')a.sort((x,y)=>delayOf(x).v-delayOf(y).v);else if(srvSort==='up')a.sort((x,y)=>{const ux=upOf(x),uy=upOf(y);return (ux&&ux.n?ux.pct:101)-(uy&&uy.n?uy.pct:101)});else if(srvSort==='name')a.sort((x,y)=>splitFlag(x).name.localeCompare(splitFlag(y).name,'ru'));return a}
function renderServers(){
  const el=$('#p-servers'); if(el.contains(document.activeElement)&&document.activeElement.tagName==='INPUT'){renderTilesOnly();return}
  const g=mainGroup(); const dead=new Set(S.dead||[]);
  const tabSrv=S.servers.filter(s=>(srvTab==='dead')===dead.has(s.tag)); const ccs={};tabSrv.forEach(s=>{const c=splitFlag(s.tag).cc||'—';ccs[c]=(ccs[c]||0)+1});if(country&&!ccs[country])country='';
  const nDead=(S.dead||[]).filter(t=>S.servers.some(s=>s.tag===t)).length;
  const thr=+store.get('thr','90'); const inW=t=>Object.values(W.groups||{}).some(x=>(x.servers||[]).includes(t));
  const sugg=S.servers.filter(s=>!dead.has(s.tag)&&!inW(s.tag)).map(s=>({t:s.tag,u:upOf(s.tag)})).filter(x=>x.u&&x.u.enough&&x.u.pct>=thr);
  window._sugg=sugg;
  el.innerHTML=`<div class="card">
    <div class="tools" style="margin-bottom:12px"><div class="seg"><button data-tab="active" class="${srvTab==='active'?'on':''}">Рабочие ${S.servers.length-nDead}</button><button data-tab="dead" class="${srvTab==='dead'?'on':''}">💤 Мёртвые ${nDead}</button></div>
      <input class="in" id="srvQ" placeholder="Поиск: страна, протокол, имя…" value="${esc(srvQ)}" style="flex:1;min-width:160px;max-width:320px">
      <select class="in" id="srvSort"><option value="score">По баллам</option><option value="delay">По пингу</option><option value="up">По доступности</option><option value="name">По имени</option><option value="def">Как в конфиге</option></select>
      <span class="sp"></span><button class="btn pri" id="testShown">⚡ ${srvTab==='dead'?'Проверить вручную':'Проверить показанные'}</button></div>
    ${srvTab==='dead'?`<div class="note" style="margin-bottom:10px">💤 Эти серверы роутер не проверяет в фоне и не берёт в кандидаты. Проверить — кнопкой выше или кликом по пингу. ↩ — вернуть. ${nDead?'<span class="lnk" data-undeadall="1">Вернуть все</span>':''}</div>`
      :sugg.length?`<div class="banner" style="margin-bottom:10px">За ${winTxt()} <b>${sugg.length}</b> серв. почти не отвечали (≥${thr}% проверок): ${sugg.slice(0,4).map(x=>esc(splitFlag(x.t).name)).join(', ')}${sugg.length>4?' и др.':''}. <button class="btn sm" data-deadall="1">💤 Перенести в мёртвые</button></div>`:''}
    <div class="chips" style="margin-bottom:12px"><span class="chip ${!country?'on':''}" data-cc="">Все ${tabSrv.length}</span>${Object.keys(ccs).sort().map(c=>`<span class="chip ${country===c?'on':''}" data-cc="${c}">${c==='—'?c:countryFlag(c)} ${c} ${ccs[c]}</span>`).join('')}</div>
    <div class="note" style="margin-bottom:8px">${g?`Клик по серверу — сделать основным в «${esc(g.tag)}» · ☆ — в сторож · 💤 — в мёртвые · i — подробно`:''}</div>
    <div class="tiles" id="tiles"></div></div>
    ${groupCard()}`;
  $('#srvSort').value=srvSort; renderTilesOnly();
}
function countryFlag(cc){return String.fromCodePoint(...[...cc].map(c=>0x1F1E6+c.charCodeAt(0)-65))}
function renderTilesOnly(){
  const box=$('#tiles'); if(!box) return; const g=mainGroup(); const w=g?wg(g.tag):{servers:[]}; const dead=new Set(S.dead||[]);
  box.innerHTML=sortTags(shownTags()).map(t=>{const s=srvInfo(t),d=delayOf(t),u=upOf(t),sc=S.scores&&S.scores[t];const sel=g&&g.override===t,auto=g&&!g.override&&g.current===t;const inw=w.servers.includes(t);
    return `<div class="tile ${sel||auto?'sel':''}" data-pick="${esc(t)}" data-group="${esc(g?g.tag:'')}"><div class="tr"><span class="nm">${srvName(t)}</span>${sel?'<span class="tag a">выбран</span>':auto?'<span class="tag g">авто</span>':''}${sc&&sc.n?scoreHTML(sc):''}${!dead.has(t)?`<span class="ico ${inw?'on':''}" data-star="${esc(t)}" title="${inw?'Убрать из сторожа':'В сторож'}">${inw?'★':'☆'}</span>`:''}${dead.has(t)?`<span class="ico" data-undead="${esc(t)}" title="Вернуть">↩</span>`:inw?'':`<span class="ico" data-dead="${esc(t)}" title="В мёртвые">💤</span>`}<span class="ib" data-info="${esc(t)}" style="width:22px;height:22px;font-size:11px">i</span></div>
      <div class="mt"><span>${esc([s.protocol,s.network].filter(Boolean).join(' · '))}</span><span class="dl" data-test1="${esc(t)}" title="Проверить">${d.h}</span></div>
      ${u?`<div class="bar" title="За ${winTxt()}: не ответил ${u.fail} из ${u.n}">${barHTML(u.bar)}</div>`:''}</div>`}).join('')||'<div class="empty">Ничего не найдено</div>';
}
function groupCard(){const g=mainGroup();if(!g)return'';const manual=g.override||'';
  return `<div class="card"><h3>Группа ${esc(g.tag)} в Xray<span class="r">${esc(g.strategy)} · ${g.members.length} серв.</span></h3><div class="note" style="margin-bottom:10px">${manual?`Сейчас выбран вручную/сторожем: <b>${srvName(manual)}</b>.`:`Режим «Авто»: Xray сам выбрал ${g.current?srvName(g.current):'—'}.`} <span class="lnk" data-pick="" data-group="${esc(g.tag)}">Переключить на «Авто»</span></div><div class="chips">${g.members.map(m=>`<span class="chip">${srvName(m)}</span>`).join('')}</div></div>`}

// ================= УСТРОЙСТВА =================
const devOpen=new Set();let devSort=(()=>{try{return JSON.parse(store.get('devSort',''))||{k:'all',d:-1}}catch(e){return {k:'all',d:-1}}})();
function renderDevices(){
  const el=$('#p-devices'); if(el.contains(document.activeElement)&&document.activeElement.tagName==='INPUT')return;
  if(!DEVS){el.innerHTML='<div class="card empty"><span class="spin"></span></div>';return}
  const now=Date.now()/1000; const list=DEVS.filter(d=>!d.mergeInto);
  {const tv=p=>p?(p.up||0)+(p.down||0):0;const key={name:d=>(d.name||d.kname||'яяя').toLowerCase(),ip:d=>d.ip.split('.').map(x=>x.padStart(3,'0')).join('.'),vpn:d=>tv(d.vpn),direct:d=>tv(d.all)-tv(d.vpn),all:d=>tv(d.all),last:d=>d.lastSeen}[devSort.k];
   if(key)list.sort((a,b)=>{const x=key(a),y=key(b);return (x<y?-1:x>y?1:0)*devSort.d})}
  const named=list.filter(d=>d.name).length;
  const tot=(p)=>p?(p.up||0)+(p.down||0):0;
  let sumV=0,sumA=0; list.forEach(d=>{sumV+=tot(d.vpn);sumA+=tot(d.all)});
  const rows=list.map(d=>{const vpn=d.vpn||{},all=d.all||{};const online=now-d.lastSeen<600;
    const v=tot(vpn),a=tot(all),dir=Math.max(0,a-v),dirC=Math.max(0,(all.conns||0)-(vpn.conns||0));const open=devOpen.has(d.id);
    let det='';
    if(open){
      const srv=Object.entries(d.servers||{}).map(([k,p])=>[k,tot(p),p.conns||0]).sort((x,y)=>(y[1]-x[1])||(y[2]-x[2]));
      const svc=Object.entries(d.services||{}).map(([k,p])=>[k,tot(p),p.conns||0]).sort((x,y)=>(y[1]-x[1])||(y[2]-x[2])).slice(0,10);
      const mxS=Math.max(1,...srv.map(x=>x[1]||x[2])),mxC=Math.max(1,...svc.map(x=>x[1]||x[2]));
      const tbl=(rows,mx,lab)=>`<table class="t"><tr><th>${lab}</th><th>Трафик</th><th>Соединений</th></tr>${rows.map(([k,b,c])=>`<tr><td>${lab==='VPN-сервер'?srvName(k,true):esc(k)}<div class="prog" style="margin-top:4px;max-width:260px"><i style="width:${Math.round((b||c)*100/mx)}%"></i></div></td><td class="mono">${b?gb(b):'—'}</td><td class="mono">${c}</td></tr>`).join('')||'<tr><td colspan=3 class="n">сегодня через VPN ничего</td></tr>'}</table>`;
      det=`<tr class="det"><td colspan="8" style="background:var(--bg2)"><div class="grid g2" style="padding:6px 2px">
        <div><div class="note" style="margin-bottom:6px">Через какие VPN-серверы шло сегодня</div>${tbl(srv,mxS,'VPN-сервер')}</div>
        <div><div class="note" style="margin-bottom:6px">Какие сервисы открывались через VPN</div>${tbl(svc,mxC,'Сервис')}</div></div>
        <div class="note" style="padding:4px 2px 2px">Напрямую (мимо VPN): ${dir?gb(dir):'—'} · ${dirC} соед. · MAC ${esc(d.isMac?d.id:'неизвестен')} · впервые замечено ${ago(d.firstSeen)}</div></td></tr>`;
    }
    return `<tr class="${open?'act':''}"><td><button class="ib" data-dopen="${esc(d.id)}" title="Подробно: через какие серверы и к каким сервисам ходило" style="transform:rotate(${open?180:0}deg);transition:transform .2s">▾</button></td>
      <td><div style="font-weight:600">${online?'<span class="dot live" style="display:inline-block;margin-right:6px"></span>':''}<span class="devic" data-dname="${esc(d.id)}" title="${esc(KINDS[d.kind]?.[1]||'')}${d.kindAuto?' (определено само — нажмите, чтобы выбрать)':''}">${devIcon(d.kind)}</span>${esc(d.name||d.kname||'Без имени')}${typeof devModeTag==='function'?devModeTag(d.ip):''} ${d.name||d.kname?'':'<span class="tag m">новое</span>'}</div><div class="note">${esc([d.name&&d.kname&&d.kname!==d.name?'в Keenetic: '+d.kname:'',d.conn||'',d.policy?'политика: '+d.policy:'',d.vendor||''].filter(Boolean).join(' · '))}</div></td>
      <td class="mono" style="font-size:12px">${esc(d.ip)}</td>
      <td class="mono"><b style="color:var(--acc)">${v?gb(v):'—'}</b><div class="n" style="font-size:11px">${vpn.conns||0} соед.</div></td>
      <td class="mono">${dir?gb(dir):'—'}<div class="n" style="font-size:11px">${dirC} соед.</div></td>
      <td class="mono">${a?gb(a):'—'}</td>
      <td class="note">${ago(d.lastSeen)}</td>
      <td><div class="mini"><button data-drule="${esc(d.id)}" title="Режим VPN, временный режим, дети, реклама">⚙</button><button data-dname="${esc(d.id)}" title="Дать имя">✎</button><button data-dmerge="${esc(d.id)}" title="Это то же устройство, что…">⇄</button><button data-ddel="${esc(d.id)}" title="Убрать из списка">✕</button></div></td></tr>${det}`}).join('');
  el.innerHTML=`<div class="card"><h3>Устройства в XKeen · сегодня<span class="r">${list.length} всего · ${list.filter(d=>now-d.lastSeen<600).length} в сети · через VPN ${gb(sumV)} · напрямую ${gb(Math.max(0,sumA-sumV))}</span></h3>
   <div class="note" style="margin-bottom:10px;line-height:1.6">Через XKeen идёт весь интернет этих устройств. XKeen делит его по правилам: сайты из списка (Instagram, YouTube, Telegram, ChatGPT…) — <b style="color:var(--acc)">через VPN</b>, всё остальное — <b>напрямую</b> через провайдера. Нажмите ▾, чтобы увидеть, через какие VPN-серверы и к каким сервисам ходило устройство. ✎ — дать имя (привяжется к MAC). ${TRF&&TRF.ctErr?'<br><span class="m">Байты по устройствам: '+esc(TRF.ctErr)+'</span>':''}</div>
   <table class="t"><thead><tr><th></th>${[['name','Устройство'],['ip','IP'],['vpn','Через VPN'],['direct','Напрямую'],['all','Всего'],['last','Последний раз']].map(([k,t])=>`<th data-dsort="${k}" style="cursor:pointer;user-select:none;white-space:nowrap">${t} ${devSort.k===k?(devSort.d>0?'▲':'▼'):'<span class="n">⇅</span>'}</th>`).join('')}<th></th></tr></thead><tbody>
   ${rows||'<tr><td colspan=8 class="empty">Пока никого не видно</td></tr>'}</tbody></table></div>
   <div class="card"><h3>iPhone появляется как новое устройство?</h3><div class="note">В iOS «Частный адрес Wi-Fi» может меняться. Настройки → Wi-Fi → ⓘ у вашей сети → «Частный адрес Wi-Fi» → <b>Фиксированный</b>. Или нажмите ⇄ у нового и выберите, с каким устройством его склеить — статистика объединится. Подписано ${named} из ${list.length}.</div></div>`;
}

// ================= КАРТА =================
function renderMap(){
  const el=$('#p-map'); if($('#flowBox')){drawFlowAll();return}
  el.innerHTML=`<div class="card"><h3>Карта соединений<span class="r mono" id="flowRate"></span></h3>
    <div class="flow" id="flowBox"><canvas id="flowCv"></canvas></div>
    <div class="legend"><span><i style="background:#fb923c"></i>устройства</span><span><i style="background:#a78bfa"></i>VPN-серверы и «напрямую»</span><span><i style="background:#22d3ee"></i>сервисы</span><span>числа — новые соединения за последние 15 минут · скорость частиц = реальный поток</span></div></div>`;
  drawFlowAll();
}
let flowAnim=null, flowModel=null;
function drawFlowAll(){if(page==='map')buildFlow('flowBox','flowCv')}
function buildFlow(boxId,cvId){
  const box=$('#'+boxId),cv=$('#'+cvId); if(!box||!cv||!FLOW) return;
  const L=FLOW.labels||{}, devN=FLOW.devices||{}, srvN=FLOW.servers||{}, svcN=FLOW.services||{};
  const topK=(o,k)=>{const e=Object.entries(o).sort((a,b)=>b[1]-a[1]);const top=e.slice(0,k).map(x=>x[0]);const rest=e.slice(k).reduce((s,x)=>s+x[1],0);return {top,rest}};
  const mob=isMobile(); const D=topK(devN,mob?6:9),V=topK(srvN,mob?5:7),C=topK(svcN,mob?6:9);
  const mapD=k=>D.top.includes(k)?k:'__other_d', mapV=k=>V.top.includes(k)?k:'__other_v', mapC=k=>C.top.includes(k)?k:'__other_c';
  const idleD=(FLOW.idle||[]).filter(k=>!D.top.includes(k)).slice(0,Math.max(0,(mob?7:10)-D.top.length));
  const cols=[[...D.top,...idleD,...(D.rest?['__other_d']:[])],[...V.top,...(V.rest?['__other_v']:[])],[...C.top,...(C.rest?['__other_c']:[])]];
  const cnt=[{...devN,__other_d:D.rest},{...srvN,__other_v:V.rest},{...svcN,__other_c:C.rest}];
  const r=box.getBoundingClientRect(), Wd=r.width, H=r.height, dpr=Math.min(2,devicePixelRatio||1);
  cv.width=Wd*dpr;cv.height=H*dpr; const ctx=cv.getContext('2d'); ctx.setTransform(dpr,0,0,dpr,0,0);
  const xs=mob?[14,Wd*.5,Wd-14]:[150,Wd*.5,Wd-210];
  if(mob) box.style.height=Math.max(420,Math.max(D.top.length,V.top.length*2,C.top.length)*58)+'px';
  const pos=cols.map((c,i)=>{const pad=34,step=c.length>1?(H-pad*2)/(c.length-1):0;const m={};c.forEach((k,j)=>m[k]={x:xs[i],y:c.length>1?pad+step*j:H/2});return m});
  box.querySelectorAll('.flowlbl').forEach(x=>x.remove());
  const colr=['#fb923c','#a78bfa','#22d3ee'];
  const name=(i,k)=>k.startsWith('__other')?'Другие':i===0?(L[k]||k):i===1?(k==='direct'?'Напрямую':splitFlag(k).flag+' '+splitFlag(k).name.replace(/\(.*\)/,'')):k;
  cols.forEach((c,i)=>c.forEach(k=>{const p=pos[i][k],d=document.createElement('div');d.className='flowlbl';
    const rate=(cnt[i][k]||0)/Math.max(60,FLOW.spanSec)*60;
    const idle=i===0&&idleD.includes(k); if(idle)d.style.opacity='.45';
    d.innerHTML=idle?`<b>${esc(name(i,k))}</b><small>нет соединений за 15 мин</small>`:`<b>${esc(name(i,k))}</b><small>${(cnt[i][k]||0)} соед. · ${rate.toFixed(1)}/мин</small>`;
    if(i===0){d.style.right=(Wd-p.x+12)+'px';d.style.textAlign='right';if(mob){d.style.right='';d.style.left=(p.x+10)+'px';d.style.textAlign='left'}}
    else if(i===2){d.style.left=(p.x+12)+'px';if(mob){d.style.left='';d.style.right=(Wd-p.x+10)+'px';d.style.textAlign='right'}}
    else{d.style.left=(p.x-60)+'px';d.style.width='120px';d.style.textAlign='center';d.style.transform=mob?'translateY(-24px)':'translateY(-34px)'}
    d.style.top=(p.y-(i===1?0:14))+'px';box.appendChild(d)}));
  const links=[];const add=(arr,ci,mapA,mapB)=>{const m={};(arr||[]).forEach(l=>{const a=mapA(l.a),b=mapB(l.b),k=a+'\u0001'+b;m[k]=(m[k]||0)+l.n});for(const k in m){const [a,b]=k.split('\u0001');if(pos[ci][a]&&pos[ci+1][b])links.push({a:pos[ci][a],b:pos[ci+1][b],n:m[k],c:colr[ci],c2:colr[ci+1]})}};
  add(FLOW.devSrv,0,mapD,mapV); add(FLOW.srvSvc,1,mapV,mapC);
  const mx=Math.max(1,...links.map(l=>l.n));
  links.forEach(l=>{l.w=1+Math.sqrt(l.n/mx)*9;l.rate=l.n/Math.max(60,FLOW.spanSec);l.parts=[];l.acc=Math.random()});
  $('#flowRate')&&($('#flowRate').textContent=`≈ ${(FLOW.total/Math.max(60,FLOW.spanSec)*60).toFixed(1)} соединений/мин`);
  flowModel={ctx,links,pos,cols,colr,Wd,H};
  cancelAnimationFrame(flowAnim); let last=performance.now();
  const curve=(l,t)=>{const x0=l.a.x,y0=l.a.y,x1=l.b.x,y1=l.b.y,cx=(x0+x1)/2,u=1-t;return [u*u*u*x0+3*u*u*t*cx+3*u*t*t*cx+t*t*t*x1,u*u*u*y0+3*u*u*t*y0+3*u*t*t*y1+t*t*t*y1]};
  const frame=now=>{const dt=Math.min(.05,(now-last)/1000);last=now;ctx.clearRect(0,0,Wd,H);
    for(const l of links){const g=ctx.createLinearGradient(l.a.x,0,l.b.x,0);g.addColorStop(0,l.c+'55');g.addColorStop(1,l.c2+'55');ctx.strokeStyle=g;ctx.lineWidth=l.w;ctx.globalAlpha=.35;ctx.beginPath();ctx.moveTo(l.a.x,l.a.y);const cx=(l.a.x+l.b.x)/2;ctx.bezierCurveTo(cx,l.a.y,cx,l.b.y,l.b.x,l.b.y);ctx.stroke();ctx.globalAlpha=1;
      if(fxOn()||true){l.acc+=dt*Math.min(12,l.rate*(fxOn()?6:2)+.15);while(l.acc>1){l.acc-=1;l.parts.push({t:0,s:.35+Math.random()*.25,o:(Math.random()-.5)*l.w*.8})}
        for(const p of l.parts){p.t+=dt*p.s;const [x,y]=curve(l,p.t);ctx.fillStyle=p.t<.5?l.c:l.c2;ctx.globalAlpha=Math.min(1,(1-p.t)*3);ctx.beginPath();ctx.arc(x,y+p.o,fxOn()?2:1.6,0,7);ctx.fill()}ctx.globalAlpha=1;l.parts=l.parts.filter(p=>p.t<1).slice(-120)}}
    for(let i=0;i<3;i++)for(const k of cols[i]){const p=pos[i][k];ctx.fillStyle=colr[i];ctx.shadowColor=colr[i];ctx.shadowBlur=fxOn()?12:0;ctx.beginPath();if(i===1){ctx.save();ctx.translate(p.x,p.y);ctx.rotate(Math.PI/4);ctx.fillRect(-6,-6,12,12);ctx.restore()}else{ctx.arc(p.x,p.y,5.5,0,7)}ctx.fill();ctx.shadowBlur=0}
    if(page==='map'&&!document.hidden) flowAnim=requestAnimationFrame(frame)};
  flowAnim=requestAnimationFrame(frame);
  if(!links.length){const d=document.createElement('div');d.className='flowlbl';d.style.cssText='left:0;right:0;top:45%;text-align:center;color:var(--muted)';d.textContent='Пока нет соединений через XKeen — карта появится, когда устройства начнут ходить в интернет';box.appendChild(d)}
}
window.addEventListener('resize',()=>{if(page==='map')drawFlowAll()});

// ================= СОБЫТИЯ =================
let evFilter='all';
function renderEvents(){const el=$('#p-events');const F={all:()=>true,bad:e=>['warn','critical'].includes(e.kind),switch:e=>['switch','restart','restored','recover'].includes(e.kind),auto:e=>e.kind==='auto',help:e=>e.kind==='help'};
  const list=(W.events||[]).filter(F[evFilter]).slice().reverse();
  el.innerHTML=`<div class="card"><h3>События<span class="r">${(W.events||[]).length} за последнее время</span></h3><div class="tools" style="margin-bottom:10px"><div class="seg">${[['all','Все'],['bad','Сбои'],['switch','Переключения'],['auto','Кандидаты'],['help','«Не грузит»']].map(([k,t])=>`<button data-evf="${k}" class="${evFilter===k?'on':''}">${t}</button>`).join('')}</div></div>${eventsList(list.slice(0,200))||'<div class="empty">Ничего нет</div>'}</div>`}

// ================= ЖУРНАЛ =================
let logT=null;const LG={file:'access',mode:'',q:'',lines:'300',auto:true};
function renderLogsShell(){const el=$('#p-logs');if($('#logBox'))return;
  el.innerHTML=`<div class="card"><div class="tools" style="margin-bottom:10px"><select class="in" id="lgFile"><option value="access">Соединения (access)</option><option value="error">Ошибки Xray</option><option value="selector">Xray Selector</option><option value="keenetic">Роутер (журнал Keenetic)</option></select>
   <select class="in" id="lgMode"><option value="">все соединения</option><option value="vpn">только через VPN</option><option value="direct">только напрямую</option><option value="block">только заблокированные</option></select>
   <span class="lgqwrap" style="flex:1;min-width:180px;position:relative;display:flex"><input class="in" id="lgQ" autocomplete="off" placeholder="Фильтр: выберите устройство или напишите: instagram, 192.168.1.50, -direct" style="flex:1"><div id="lgSug" class="sugg" style="display:none"></div></span><select class="in" id="lgSince" title="За какой период"><option value="0">за всё время</option><option value="5">5 минут</option><option value="15">15 минут</option><option value="60">1 час</option><option value="180">3 часа</option><option value="1440">сутки</option></select><select class="in" id="lgLines"><option>100</option><option selected>300</option><option>1000</option></select>
   <label class="sw"><input type="checkbox" id="lgAuto" checked><span class="k"></span>авто</label></div><div class="note" id="lgInfo" style="margin-bottom:6px"></div><div class="log" id="logBox" style="max-height:calc(100vh - 230px)"></div></div>`;
  syncLogTab();['lgFile','lgMode','lgLines','lgAuto','lgSince'].forEach(id=>$('#'+id).addEventListener('change',loadLogs));let qt;$('#lgQ').addEventListener('input',()=>{clearTimeout(qt);qt=setTimeout(loadLogs,350)});loadLogs()}
// вкладки Журнала: Xray (соединения | ошибки), Роутер, Программа
function syncLogTab(){const f=$('#lgFile');if(!f)return;const x=pageFilter==='access';
  [...f.options].forEach(o=>{o.hidden=x?!['access','error'].includes(o.value):o.value!==pageFilter});
  if(!x||!['access','error'].includes(f.value)) f.value=pageFilter||'access'; f.style.display=x?'':'none'}
async function loadLogs(){clearTimeout(logT);if(page!=='logs'||!$('#logBox'))return;const f=$('#lgFile').value,q=$('#lgQ').value.trim();$('#lgMode').disabled=f!=='access';$('#lgMode').style.display=f==='access'?'':'none';
  if(!$('#lgAllWrap')){const w=document.createElement('label');w.id='lgAllWrap';w.className='svc-i';w.innerHTML='<input type="checkbox" id="lgAll"> весь журнал';$('#lgMode').after(w);$('#lgAll').checked=store.get('lgAll','')==='1';$('#lgAll').onchange=()=>{store.set('lgAll',$('#lgAll').checked?'1':'');loadLogs()}}
  $('#lgAllWrap').style.display=f==='keenetic'?'':'none';$('#lgAllWrap').title='Без галочки — только важное для интернета и VPN: провайдеры, WireGuard/прокси, DNS, новые устройства, перезагрузки, ошибки';
  try{const r=await api(`/api/logs?file=${f}&lines=${$('#lgLines').value}&mode=${f==='access'?$('#lgMode').value:''}&since=${$('#lgSince').value}&q=${encodeURIComponent(q)}${f==='keenetic'&&!$('#lgAll').checked?'&imp=1':''}`);const names=r.names||{};const box=$('#logBox');const atB=box.scrollTop+box.clientHeight>=box.scrollHeight-30;
    box.innerHTML=(r.lines||[]).map(l=>hl(l,r.devices?'':q,names)).join('\n')||'<span class="ld">— пусто —</span>';if(atB)box.scrollTop=box.scrollHeight;$('#lgInfo').textContent=r.error?'Не удалось прочитать '+r.path+': '+r.error:`${r.path} · показано ${(r.lines||[]).length} из последних ${r.total} строк${r.devices?' · фильтр по устройству':''} · время — ${tzName(W.settings&&W.settings.tzOffsetMin)}`}catch(e){$('#lgInfo').textContent='Ошибка: '+e.message}
  if($('#lgAuto').checked)logT=setTimeout(loadLogs,3000)}
function hl(line,q,names){let h=esc(line);for(const ip in names){h=h.split(' '+esc(ip)+':').join(' <span class="dv">'+esc(names[ip])+'</span>:')}
  if(/-&gt; direct\]/.test(h))h=`<span class="ld">${h}</span>`;else if(/-&gt; block\]/.test(h))h=`<span class="lb">${h}</span>`;else if(/accepted/.test(h))h=`<span class="lv">${h}</span>`;else if(/\[(Error|Warning)\]|error|failed/i.test(h))h=`<span class="le">${h}</span>`;
  for(const t of (q||'').split(/\s+/).filter(x=>x&&!x.startsWith('-'))){const re=new RegExp('('+t.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')+')(?![^<]*>)','gi');h=h.replace(re,'<mark>$1</mark>')}return h}

// ================= НАСТРОЙКИ =================
function renderSettings(){const el=$('#p-settings');if(el.contains(document.activeElement)&&['INPUT','TEXTAREA','SELECT'].includes(document.activeElement.tagName))return;
  const st=W.settings||{}; const th=store.get('theme','xrock'), fx=store.get('fx','auto');
  const tgOK=st.tgToken&&st.tgChatID;
  el.innerHTML=`
  <div class="card" data-st="main"><h3>Оформление</h3><div class="tools"><span class="note">Тема:</span><div class="seg">${[['xrock','Ночная'],['srm','Светлая (SRM)']].map(([k,t])=>`<button data-settheme="${k}" class="${th===k?'on':''}">${t}</button>`).join('')}</div>
   <span class="note" style="margin-left:14px">Эффекты:</span><div class="seg">${[['auto','Авто'],['full','Полные'],['lite','Лёгкие']].map(([k,t])=>`<button data-fx="${k}" class="${fx===k?'on':''}">${t}</button>`).join('')}</div></div>
   <div class="note" style="margin-top:8px">«Авто»: на компьютере — живой фон, стекло, 3D-наклон карточек и частицы на карте; на телефоне — лёгкая версия. Тема и эффекты запоминаются на каждом устройстве отдельно.</div></div>
  <div class="card x-exp" data-st="main"><h3>Сторож — тонкая настройка</h3><div class="fg">
   <label class="full">Что проверять — по строке «Название = адрес»<textarea class="in" id="sTargets">${esc((st.targets||[]).map(t=>t.name+' = '+t.url).join('\n'))}</textarea></label>
   <label>Проверять активный каждые, сек<input class="in" id="sInt" type="number" min="5" value="${st.intervalSec||15}"></label>
   <label>Проверять запасные каждые, сек<input class="in" id="sRes" type="number" min="15" value="${st.reserveEverySec||60}"></label>
   <label>Проверять подключения Keenetic каждые, сек<input class="in" id="sTun" type="number" min="30" value="${st.tunEverySec||60}"></label>
   <label>Таймаут запроса, мс<input class="in" id="sTo" type="number" min="1000" step="500" value="${st.timeoutMs||6000}"></label>
   <label>Переключать после неудач подряд<input class="in" id="sFails" type="number" min="1" value="${st.failsToSwitch||2}"></label>
   <label>Адрес для замера скорости<input class="in" id="sSpUrl" value="${esc(st.speedURL||'')}"></label>
   <label>Замер активного каждые, сек<input class="in" id="sSpA" type="number" min="10" value="${st.speedActiveSec||30}"></label>
   <label>Замер запасных каждые, сек<input class="in" id="sSpE" type="number" min="30" value="${st.speedEverySec||150}"></label>
   <label>Минимальная скорость, Мбит/с<input class="in" id="sSpMin" type="number" step="0.5" value="${(st.speedMinKbps||0)/1000}"></label>
   <label>Возврат на приоритетный через, мин<input class="in" id="sRet" type="number" min="1" value="${Math.round((st.returnAfterSec||600)/60)}"></label>
   <label>Основных 📌<input class="in" id="sMain" type="number" min="1" max="12" value="${st.mainSlots||6}"></label>
   <label>Запасных 🟡<input class="in" id="sResv" type="number" min="0" max="6" value="${st.reserveSlots??3}"></label>
   <label>На проверке 🧪<input class="in" id="sCand" type="number" min="0" max="10" value="${st.candSlots??5}"></label>
   <label>Проверять кандидата, мин<input class="in" id="sTrial" type="number" min="30" step="10" value="${st.trialMin||120}"></label>
   <label>Проходной балл<input class="in" id="sPass" type="number" min="1" max="100" value="${st.passScore||70}"></label>
   <label>Пауза между перезапусками, мин<input class="in" id="sRcd" type="number" min="5" value="${st.restartCooldownMin||15}"></label>
   <label class="full"><span class="sw"><input type="checkbox" id="sRetOn" ${st.returnToFirst?'checked':''}><span class="k"></span>Возвращаться на более приоритетный сервер (по порядку списка — только когда умный выбор выключен)</span></label>
   <label class="full"><span class="sw"><input type="checkbox" id="sSmart" ${!st.noSmartSpeed?'checked':''}><span class="k"></span>Умный выбор: держаться сервера с наибольшими баллами за час (переход — если лучше на 5+ баллов и здоров 5+ мин; ждёт, пока интернетом не пользуются, но не дольше 10 мин; не чаще раза в 20 мин)</span></label>
   <label class="full"><span class="sw"><input type="checkbox" id="sRst" ${st.restartOnSwitch?'checked':''}><span class="k"></span>Перезапускать XKeen после переключения из-за сбоя (обрывает «зависшие» соединения)</span></label>
   <label class="full"><span class="sw"><input type="checkbox" id="sSync" ${!st.noBalancerSync?'checked':''}><span class="k"></span>Переписывать состав группы в конфиге Xray по списку сторожа</span></label>
  </div><div class="tools" style="margin-top:12px"><button class="btn pri" id="sSave">Сохранить</button></div></div>
  <div class="card" data-st="tg"><h3>Telegram ${tgOK?'<span class="tag g">● группа подключена</span>':'<span class="tag">не подключён</span>'} ${st.tgAdminID?'<span class="tag g">● личка: '+esc(st.tgAdminName||'')+'</span>':''}</h3>
   <div class="note" style="margin-bottom:10px;line-height:1.6"><b>Группа</b> получает только «VPN не работает 5+ минут» / «VPN снова работает» и закреплённый статус с кнопкой «🆘 Не грузит». <b>Личка</b> — всё техническое (без звука, пачками), утренняя сводка и управление: /switch, /restart, /traffic, /devices. Бот должен быть администратором группы с правами «Закреплять» и «Удалять сообщения».</div>
   <div class="tools"><input class="in" id="tgTok" placeholder="Токен бота от @BotFather" value="${esc(st.tgToken||'')}" style="flex:1;max-width:420px"><button class="btn pri" data-tg="test" title="Бот найдёт группу, где ему написали /start; если группы нет — личный чат">Подключить</button></div>
   <div class="tools" style="margin-top:8px"><button class="btn" data-tg="admin">🔐 Подключить мою личку</button><span class="note">сначала напишите боту /start в личном чате</span><span class="sp"></span><button class="btn" data-tg="sendtest">🧪 Отправить тест</button><button class="btn" data-tg="sample" title="Проходит тот же путь, что и настоящие уведомления; придёт примерно через минуту">📨 Пример уведомления</button><button class="btn" data-tg="pin">📌 Пересоздать закреплённый статус</button><button class="btn" data-tg="clear">Отключить</button></div>
   <div id="tgStat" class="note" style="margin-top:8px"></div>
   <div class="fg" style="margin-top:12px">
    <label>Что присылать<select class="in" id="tgEv"><option value="crit">Только аварии (VPN не работает / снова работает)</option><option value="important">Важное: аварии и смена сервера (по умолчанию)</option><option value="all">Всё, включая проверку кандидатов</option><option value="off">Ничего (пауза)</option></select></label>
    <label>Часовой пояс (журналы, Telegram, сводка)<select class="in" id="tzOff">${TZS.map(([m,n])=>`<option value="${m}" ${(st.tzOffsetMin??180)===m?'selected':''}>${n}</option>`).join('')}</select></label>
    <label>Тихие часы с<input class="in" id="qFrom" type="number" min="0" max="23" value="${st.quietFrom??23}"></label>
    <label>до<input class="in" id="qTo" type="number" min="0" max="23" value="${st.quietTo??8}"></label>
    <label>Утренняя сводка в (час, -1 — нет)<input class="in" id="dgH" type="number" min="-1" max="23" value="${st.digestHour??9}"></label>
    <label class="full"><span class="sw"><input type="checkbox" id="qOn" ${st.quietOn?'checked':''}><span class="k"></span>Тихие часы: ночью в группу ничего, в личку — только важное и без звука</span></label>
    <label class="full"><span class="sw"><input type="checkbox" id="pinOn" ${st.tgPinStatus?'checked':''}><span class="k"></span>Закреплённый «живой» статус в группе</span></label>
    <label class="full"><span class="sw"><input type="checkbox" id="clOn" ${st.tgCleanGroup?'checked':''}><span class="k"></span>Удалять в группе /status и ответ через минуту</span></label>
   </div><div class="tools" style="margin-top:10px"><button class="btn pri" id="sSave2">Сохранить</button></div></div>
  <div class="card x-exp" data-st="main"><h3>Трафик</h3><div class="note" style="margin-bottom:10px">Точные гигабайты по серверам считает сам Xray — для этого в его конфиг нужно добавить статистику (программа сделает это сама, с резервной копией) и перезапустить Xray. По устройствам — по счётчикам соединений роутера: ${TRF?(TRF.ctOK?'<span class="g">работает</span>':'<span class="m">'+esc(TRF.ctErr||'нет данных')+'</span>'):'…'}.</div>
   <div class="tools">${TRF&&TRF.statsConfigured?`<span class="tag g">✓ статистика Xray включена${TRF.statsOK?'':' — ждёт перезапуска Xray'}</span>`:'<button class="btn pri" id="enStats">Включить точный трафик по серверам</button>'}
   ${TRF&&TRF.tgRoute?'<span class="tag g">✓ адреса Telegram идут через VPN</span>':'<button class="btn" id="enTgRoute">Пустить адреса Telegram через VPN</button>'}</div></div>
  <div class="card" data-st="main"><h3>Резервная копия настроек</h3><div class="note" style="margin-bottom:10px">Сторож, мёртвые серверы, бот, настройки и имена устройств — одним файлом.</div>
   <div class="tools"><a class="btn" href="/api/backup" download>⬇ Скачать копию</a><label class="btn">⬆ Восстановить из файла<input type="file" id="restoreFile" accept=".json" style="display:none"></label></div></div>
  <div class="card" data-st="main"><h3>Приложение на телефоне</h3><div class="note" style="line-height:1.6">iPhone: откройте эту страницу в Safari → «Поделиться» → <b>«На экран Домой»</b>. Компьютер (Chrome/Edge): значок «Установить» в адресной строке. Панель откроется как отдельное приложение со своей иконкой.</div></div>`;
  $('#tgEv').value=st.tgEvents==='off'?'off':(st.tgLevel||'important');
  filterCards(el);
}
async function saveSettings(){const st=W.settings||{};
  const targets=$('#sTargets').value.split('\n').map(l=>l.trim()).filter(Boolean).map(l=>{const i=l.indexOf('=');return i>0?{name:l.slice(0,i).trim(),url:l.slice(i+1).trim()}:{name:'',url:l}});
  const s={...st,targets,intervalSec:+$('#sInt').value,reserveEverySec:+$('#sRes').value,tunEverySec:+$('#sTun').value,noSmartSpeed:!$('#sSmart').checked,timeoutMs:+$('#sTo').value,failsToSwitch:+$('#sFails').value,speedURL:$('#sSpUrl').value.trim(),speedActiveSec:+$('#sSpA').value,speedEverySec:+$('#sSpE').value,
    speedMinKbps:Math.round(+$('#sSpMin').value*1000),returnAfterSec:Math.round(+$('#sRet').value*60),candSlots:+$('#sCand').value,mainSlots:+$('#sMain').value,reserveSlots:+$('#sResv').value,trialMin:+$('#sTrial').value,passScore:+$('#sPass').value,restartCooldownMin:+$('#sRcd').value,
    returnToFirst:$('#sRetOn').checked,restartOnSwitch:$('#sRst').checked,noBalancerSync:!$('#sSync').checked,tgEvents:$('#tgEv').value==='off'?'off':'all',tgLevel:$('#tgEv').value==='off'?(st.tgLevel||'important'):$('#tgEv').value,quietFrom:+$('#qFrom').value,tzOffsetMin:+$('#tzOff').value,quietTo:+$('#qTo').value,digestHour:+$('#dgH').value,
    quietOn:$('#qOn').checked,tgPinStatus:$('#pinOn').checked,tgCleanGroup:$('#clOn').checked,tzOffsetMin:-new Date().getTimezoneOffset()};
  if(await wpost({settings:s}))toast('Сохранено');}

// ================= ОБРАБОТЧИКИ =================
document.addEventListener('click',async e=>{
  const T=s=>e.target.closest(s);let t;
  if(t=T('[data-go]')){e.preventDefault();go(t.dataset.go);return}
  if(t=T('[data-info]')){e.stopPropagation();openServer(t.dataset.info);return}
  if(t=T('[data-settheme]')){store.set('theme',t.dataset.settheme);applyTheme();renderSettings();return}
  if(t=T('[data-fx]')){store.set('fx',t.dataset.fx);applyTheme();renderSettings();return}
  if(t=T('[data-evf]')){evFilter=t.dataset.evf;renderEvents();return}
  if(t=T('[data-tab]')){srvTab=t.dataset.tab;store.set('srvTab',srvTab);country='';renderServers();return}
  if(t=T('[data-cc]')){country=t.dataset.cc;renderServers();return}
  if(t=T('[data-test1]')){e.stopPropagation();testTags([t.dataset.test1]);return}
  if(t=T('[data-star]')){e.stopPropagation();const g=mainGroup();if(!g)return;const l=[...wg(g.tag).servers];const i=l.indexOf(t.dataset.star);if(i>=0)l.splice(i,1);else l.push(t.dataset.star);wpost({group:g.tag,servers:l});toast(i>=0?'Убран из сторожа':'Добавлен в сторож');return}
  if(t=T('[data-dead]')){e.stopPropagation();setDead([t.dataset.dead],true);return}
  if(t=T('[data-undead]')){e.stopPropagation();setDead([t.dataset.undead],false);return}
  if(t=T('[data-deadall]')){if(confirm(`Перенести ${window._sugg.length} серв. в мёртвые? Роутер перестанет их проверять в фоне.`))setDead(window._sugg.map(x=>x.t),true);return}
  if(t=T('[data-undeadall]')){setDead(S.dead||[],false);return}
  if(t=T('[data-pick]')){pick(t.dataset.group,t.dataset.pick);return}
  if(t=T('[data-wmove]')){const g=t.dataset.wg,i=+t.dataset.wmove,d=+t.dataset.dir,l=[...wg(g).servers];[l[i],l[i+d]]=[l[i+d],l[i]];wpost({group:g,servers:l});return}
  if(t=T('[data-wdel]')){const g=t.dataset.wg;wpost({group:g,servers:wg(g).servers.filter(x=>x!==t.dataset.wdel)});if(drawerTag===t.dataset.wdel)closeOverlays();return}
  if(t=T('[data-wadd1]')){const g=t.dataset.wg;wpost({group:g,servers:[...wg(g).servers,t.dataset.wadd1]});toast('Добавлен в сторож');closeOverlays();return}
  if(t=T('[data-wpick]')){openServerPicker(t.dataset.wpick);return}
  if(t=T('[data-wadd]')){const g=t.dataset.wadd,v=document.getElementById('wadd-'+g).value;if(!v){toast('Выберите сервер');return}wpost({group:g,servers:[...wg(g).servers,v]});return}
  if(t=T('[data-wlock]')){const g=t.dataset.wg,s=t.dataset.wlock,on=(wg(g).locked||[]).includes(s);wpost(on?{group:g,unlock:s}:{group:g,lock:s});return}
  if(t=T('[data-wpin]')){const g=t.dataset.wg,s=t.dataset.wpin,on=(wg(g).pinned||[]).includes(s);wpost(on?{group:g,unpin:s}:{group:g,pin:s});return}
  if(t=T('[data-wresetex]')){wpost({group:t.dataset.wresetex,resetExcluded:true});return}
  if(t=T('[data-wcheck]')){wpost({group:t.dataset.wcheck,checkNow:true});toast('Проверяю…');return}
  if(t=T('[data-bsync]')){if(await wpost({group:t.dataset.bsync,syncNow:true}))toast('Группа в конфиге обновлена');return}
  if(t=T('[data-xrestart]')){restartXray();return}
  if(t=T('#testShown')){testTags(shownTags());return}
  if(t=T('#sSave')||T('#sSave2')){saveSettings();return}
  if(t=T('[data-tg]')){const a=t.dataset.tg;if(a==='clear'&&!confirm('Отключить бота?'))return;t.disabled=true;try{const r=await api('/api/telegram',{token:($('#tgTok')||{}).value||'',action:a});if(a==='sample'){toast('Пример придёт примерно через минуту');t.disabled=false;return}
    if(a==='sendtest'){toast(r.ok?'Тест отправлен — проверьте Telegram':'Не всё доставлено');tgStatus(r);t.disabled=false;return}
    toast(a==='test'?'Подключено'+(r.chat?': '+r.chat:''):a==='admin'?'Личка подключена: '+(r.name||''):a==='pin'?'Статус закреплён':'Отключено');loadState()}catch(err){alert(err.message)}t.disabled=false;return}
  if(t=T('#enTgRoute')){if(!confirm('Добавить в правила XKeen адреса Telegram (через VPN)? Будет резервная копия, правило проверится самим Xray, затем Xray перезапустится — связь пропадёт на 5–10 секунд.'))return;try{await api('/api/tg-route',{});alert('Готово, Xray перезапускается');loadTraffic()}catch(err){alert(err.message)}return}
  if(t=T('#enStats')){if(!confirm('Добавить статистику трафика в конфиг Xray? Будет сделана резервная копия. После этого нужен перезапуск Xray.'))return;try{await api('/api/xray-stats',{});if(confirm('Готово. Перезапустить Xray сейчас?'))restartXray();loadTraffic()}catch(err){alert(err.message)}return}
  if(t=T('[data-dsort]')){const k=t.dataset.dsort;devSort=devSort.k===k?{k,d:-devSort.d}:{k,d:(k==='name'||k==='ip')?1:-1};store.set('devSort',JSON.stringify(devSort));renderDevices();return}
  if(t=T('[data-dopen]')){const id=t.dataset.dopen;devOpen.has(id)?devOpen.delete(id):devOpen.add(id);renderDevices();return}
  if(t=T('[data-tunall]')){const all=(TUN&&TUN.tunnels||[]).map(r=>r.iface.name);if(!all.length)return;if(!confirm('Взять все подключения Keenetic ('+all.length+') в резерв и сразу проверить? Xray перезапустится (5–10 с).'))return;t.disabled=true;t.textContent='применяю…';try{await api('/api/reserve',{ifaces:all,auto:true});await api('/api/tunnels',{});toast('Готово, идёт проверка');setTimeout(loadTunnels,15000)}catch(err){toast('Ошибка: '+err.message)}t.disabled=false;t.textContent='⇩ Взять все в резерв';return}
  if(t=T('[data-tuncheck]')){t.disabled=true;t.textContent='проверяю…';await api('/api/tunnels',{});setTimeout(loadTunnels,15000);return}
  if(t=T('[data-drule]')){const d=DEVS.find(x=>x.id===t.dataset.drule);if(d)openDevRules(d);return}
  if(t=T('[data-dname]')){const d=DEVS.find(x=>x.id===t.dataset.dname);if(d)openDevEditor(d);return}
  if(t=T('[data-dmerge]')){const others=DEVS.filter(x=>x.id!==t.dataset.dmerge&&x.name&&!x.mergeInto);if(!others.length){toast('Сначала дайте имена другим устройствам');return}
    const n=prompt('С каким устройством склеить? Напишите его имя:\n'+others.map(x=>'• '+x.name).join('\n'));const tgt=others.find(x=>x.name.toLowerCase()===String(n||'').trim().toLowerCase());if(n!==null&&!tgt){toast('Не нашёл такое имя');return}if(tgt){await api('/api/devices',{id:t.dataset.dmerge,mergeInto:tgt.id});toast('Склеено с '+tgt.name);loadDevices()}return}
  if(t=T('[data-ddel]')){if(confirm('Убрать устройство из списка? Если оно снова появится в сети — вернётся как новое.')){await api('/api/devices',{id:t.dataset.ddel,delete:true});loadDevices()}return}
});
document.addEventListener('change',e=>{const t=e.target;
  if(t.matches('[data-wtoggle]'))wpost({group:t.dataset.wtoggle,enabled:t.checked});
  if(t.matches('[data-wauto]')){wpost({group:t.dataset.wauto,auto:t.checked});if(t.checked)toast('Автокандидаты включены: текущие серверы стали стабильными')}
  if(t.id==='srvSort'){srvSort=t.value;store.set('sort2',srvSort);renderTilesOnly()}
  if(t.id==='restoreFile'&&t.files[0]){t.files[0].text().then(async x=>{try{const j=JSON.parse(x);const n=Object.keys(j.xrayConfigs||{}).length;const withCfg=n&&confirm('В копии есть конфиги Xray ('+n+' файлов). Восстановить и их? Xray перезапустится, с проверкой и откатом.\n\nОтмена — восстановить только настройки панели.');
  const r=await api('/api/backup',{state:j.state,devices:j.devices,rules:j.rules,xrayConfigs:withCfg?j.xrayConfigs:undefined});toast(r.cfgError?'Настройки восстановлены, конфиги — нет: '+r.cfgError:'Восстановлено');loadState()}catch(err){alert('Не удалось: '+err.message)}})}
});
document.addEventListener('input',e=>{if(e.target.id==='srvQ'){srvQ=e.target.value;renderTilesOnly()}});
async function testTags(tags){if(!tags.length)return;try{await api('/api/test',{tags});S.testing=[...new Set([...S.testing,...tags])];renderPage();setTimeout(loadState,800)}catch(e){toast('Ошибка: '+e.message)}}
async function pick(group,target){if(!group){toast('Нет группы');return}try{await api('/api/select',{group,target});toast(target?'Основной: '+splitFlag(target).name:'Режим «Авто»');closeOverlays();loadState()}catch(e){toast('Не удалось: '+e.message)}}
async function setDead(tags,dead){try{const r=await api('/api/dead',{tags,dead});toast(dead?`В мёртвых: +${r.changed}`+(r.skipped&&r.skipped.length?` (${r.skipped.length} в стороже — пропущены)`:''):`Возвращено: ${r.changed}`);closeOverlays();loadState()}catch(e){toast('Ошибка: '+e.message)}}

const TZS=[[120,'Калининград (UTC+2)'],[180,'Москва (UTC+3)'],[240,'Самара (UTC+4)'],[300,'Екатеринбург (UTC+5)'],[360,'Омск (UTC+6)'],[420,'Новосибирск, Красноярск (UTC+7)'],[480,'Иркутск (UTC+8)'],[540,'Якутск (UTC+9)'],[600,'Владивосток (UTC+10)'],[660,'Магадан, Сахалин (UTC+11)'],[720,'Камчатка (UTC+12)'],[0,'UTC']];
function tzName(m){const x=TZS.find(t=>t[0]===(m??180));return x?x[1]:'UTC'+(m>=0?'+':'')+(m/60)}
</script>
