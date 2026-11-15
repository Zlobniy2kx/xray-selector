<script>
"use strict";
// ================= ПРАВИЛА (2.3) =================
// Каталог сервисов: «Через VPN» / «Без VPN — через вашего провайдера» / по умолчанию.
let RUL=null, RD=null; // RD — черновик: {mode,vpn:Set,novpn:Set,quicOff,ads}
async function loadRules(){try{RUL=await api('/api/rules')}catch(e){RUL={error:e.message}}
  if(RUL&&RUL.state&&(!RD||!RD.dirty))RD=draftFrom(RUL.state);if(page==='sites')renderPage()}
function draftFrom(st){return {mode:st.mode||1,vpn:new Set(st.vpn||[]),novpn:new Set(st.novpn||[]),quicOff:!!st.quicOff,ads:!!st.ads,dirty:false}}
const MODES=[[1,'Только выбранное','Через VPN работают только сервисы, которые вы отметили ниже (например, YouTube и Instagram). Все остальные сайты — как обычно, без VPN. Интернет самый быстрый.'],
  [2,'Всё, кроме российского','Через VPN работают все сайты. Без VPN — только банки, Госуслуги и другие сайты из списка «Без VPN» справа, чтобы они не блокировали вход.'],
  [3,'Абсолютно всё','Через VPN работают все сайты без исключений. Банки и Госуслуги тоже пойдут через VPN — они могут не открыться или попросить подтверждение.']];

// ---- каталог (используется и в мастере) ----
// opts: {sel:Set, groups:[id], ids:'vpn'|'novpn', counts:{id:n}, prefix}
function catalogHTML(services,groups,sel,opts={}){
  const byG={};services.forEach(s=>{(byG[s.group]=byG[s.group]||[]).push(s)});
  const box=(ids,label,cls)=>{const n=ids.filter(id=>sel.has(id)).length;const st=n===0?'':n===ids.length?'checked':'data-ind="1"';
    return `<label class="svc-i ${cls||''}"><input type="checkbox" data-cgrp="${ids.join(',')}" ${st}> ${label}</label>`};
  const svcRow=s=>{const c=opts.counts&&opts.counts[s.id];return `<label class="svc-i" title="${esc(s.hint||'')}"><input type="checkbox" data-csvc="${s.id}" ${sel.has(s.id)?'checked':''}> ${esc(s.name)}${s.hint?qh(s.hint):''}${c?` <span class="tag" title="записей в ваших правилах">${c}</span>`:''}</label>`};
  const all=groups.flatMap(g=>(byG[g]||[]).map(s=>s.id));
  return `<div class="tools" style="margin-bottom:8px">${box(all,'<b>Выбрать все</b>')}<span class="note">${all.filter(id=>sel.has(id)).length} из ${all.length}</span></div>`+
    groups.map(g=>{const l=byG[g]||[];if(!l.length)return '';const gn=(RUL&&RUL.groups||svcGroups0).find(x=>x.id===g);
      return `<div class="svc-g"><div class="svc-h">${l.length>=4?box(l.map(s=>s.id),'<b>'+esc(gn?gn.name:g)+'</b>'):'<b>'+esc(gn?gn.name:g)+'</b>'}</div><div class="svc-l">${l.map(svcRow).join('')}</div></div>`}).join('');
}
const svcGroups0=[{id:'popular',name:'Популярные'},{id:'social',name:'Соцсети и мессенджеры'},{id:'ai',name:'Нейросети'},{id:'media',name:'Видео и музыка'},{id:'work',name:'Работа'},{id:'games',name:'Игры'},{id:'other',name:'Прочее'},{id:'novpn',name:'Без VPN'}];
// состояние «частично» у групповых галочек
function fixIndeterminate(root){(root||document).querySelectorAll('[data-ind]').forEach(x=>{x.indeterminate=true})}
// клик по каталогу: target — Set, который меняем
function catalogClick(e,sel,onChange){const g=e.target.closest('[data-cgrp]'),s=e.target.closest('[data-csvc]');
  if(g){const ids=g.dataset.cgrp.split(',');const on=g.checked;ids.forEach(id=>on?sel.add(id):sel.delete(id));onChange();return true}
  if(s){s.checked?sel.add(s.dataset.csvc):sel.delete(s.dataset.csvc);onChange();return true}return false}

function renderRulesPage(){const el=$('#p-sites');
  if(el.contains(document.activeElement)&&['INPUT','TEXTAREA','SELECT'].includes(document.activeElement.tagName)&&document.activeElement.type!=='checkbox')return;
  if(!$('#rulesTop')){el.innerHTML='<div id="rulesTop"></div><div class="card" style="padding:12px 16px"><div class="tools"><b style="font-size:15px">Свои сайты и все правила Xray</b><span class="note">домены, ссылки и IP — через VPN, напрямую или блокировать; правила панели здесь не показываются</span></div></div><div id="sitesOld"></div>'}
  if(!RUL){if(!loadRules._busy){loadRules._busy=1;loadRules().finally(()=>loadRules._busy=0)}$('#rulesTop').innerHTML='<div class="card empty"><span class="spin"></span></div>'}
  else $('#rulesTop').innerHTML=rulesTopHTML();
  fixIndeterminate($('#rulesTop'));
  renderSites();
}
function rulesChangesText(){if(!RUL||!RD)return '';const st=RUL.state,o=[];
  if(RD.mode!==(st.mode||1))o.push('режим: «'+MODES[RD.mode-1][1]+'»');
  const df=(a,b)=>[...a].filter(x=>!b.includes(x)).length+b.filter(x=>!a.has(x)).length;
  const v=df(RD.vpn,st.vpn||[]),n=df(RD.novpn,st.novpn||[]);if(v)o.push('через VPN: '+v+' изм.');if(n)o.push('без VPN: '+n+' изм.');
  if(RD.quicOff!==!!st.quicOff)o.push('QUIC');if(RD.ads!==!!st.ads)o.push('реклама');return o.join(' · ')}
function rulesTopHTML(){const R=RUL;if(R.error)return `<div class="card"><div class="banner">${esc(R.error)}</div></div>`;
  const svcs=R.services||[],D=RD;const counts={};Object.entries(R.byService||{}).forEach(([k,v])=>counts[k]=v.length);
  const vpnGroups=['popular','social','ai','media','work','games','other'];
  const changed=D.dirty;
  return `
  ${R.absorbable?`<div class="card"><div class="banner" style="display:flex;gap:10px;align-items:center;flex-wrap:wrap"><span>В ваших правилах <b>${R.absorbable}</b> записей относятся к сервисам из каталога. Разнести их по сервисам? Остальные ваши сайты останутся как есть.</span><button class="btn pri" id="rulAbsorb">Разнести по сервисам</button></div></div>`:''}
  ${(R.suggest||[]).length?`<div class="card"><h3>Пустить через VPN? ${qh('Эти сайты часто открывают ваши устройства, но через провайдера они не открываются, а через VPN — открываются. Это только предложение: ничего не меняется, пока вы не нажмёте.')}</h3>
    ${R.suggest.map(x=>`<div class="row"><span><b>${esc(x.host)}</b> <span class="note">${x.n} обращений · проверено ${ago(x.t)}</span></span><span class="tools"><button class="btn sm pri" data-addvpn="${esc(x.host)}">Через VPN</button><button class="btn sm" data-rignore="${esc(x.host)}">Скрыть</button></span></div>`).join('')}</div>`:''}
  <div class="card"><h3>Что пускать через VPN ${qh('Это настройка для всех устройств дома. Для отдельного телефона или компьютера её можно поменять в разделе «Устройства» → ⚙.')}</h3>
   <div class="grid g3">${MODES.map(([m,t,dsc])=>{const saved=(R.state.mode||1)===m,sel=D.mode===m,pending=sel&&!saved;
     return `<label class="modecard ${sel?'on':''} ${pending?'pend':''}"><input type="radio" name="rmode" value="${m}" ${sel?'checked':''}><b>${t}</b>${saved?' <span class="tag g">✓ включён</span>':''}<div class="note">${dsc}</div>
      ${pending?`<div class="tools" style="margin-top:10px"><button class="btn pri sm" id="rulSave2">Включить этот режим</button><button class="btn sm" id="rulReset2">Отмена</button></div>`:''}</label>`}).join('')}</div>
   <div class="note" style="margin-top:8px">${D.mode!==(R.state.mode||1)?'Режим выбран, но ещё не включён — нажмите «Включить этот режим».':'Чтобы сменить режим, нажмите на другой вариант, затем «Включить этот режим».'}</div></div>
  <div class="grid g2">
   <div class="card"><h3>Через VPN ${qh('Отметьте сервисы, которые без VPN не работают или работают плохо.')}</h3>
    ${D.mode===3?'<div class="note" style="margin-bottom:8px">Сейчас выбрано «Абсолютно всё» — через VPN и так идут все сайты. Отметки пригодятся, если поменяете режим.</div>':''}
    <div id="catVPN">${catalogHTML(svcs,vpnGroups,D.vpn,{counts})}</div></div>
   <div>
    <div class="card"><h3>Без VPN — через вашего провайдера ${qh('Банки, Госуслуги и российские магазины часто не пускают с зарубежных адресов.')}</h3>
     <div class="note" style="margin-bottom:8px">Эти сайты всегда открываются через ваш обычный интернет, как будто VPN нет. Так банки и Госуслуги не блокируют вход «из-за границы», а игровые серверы дают меньший пинг.${D.mode===3?' <b>Сейчас выбрано «Абсолютно всё» — этот список не действует.</b>':''}</div>
     <div id="catNO">${catalogHTML(svcs,['novpn'],D.novpn,{counts})}</div></div>
    <div class="card"><h3>Дополнительно</h3>
     <label class="sw" style="display:flex;margin-bottom:8px"><input type="checkbox" id="rQuic" ${D.quicOff?'checked':''}><span class="k"></span>Отключить QUIC (рекомендуется) ${qh('QUIC — это новый протокол поверх UDP (YouTube, Google, Instagram). Через VPN он часто работает с рывками или зависает. Если его отключить, приложения сами перейдут на обычный TCP — видео грузится ровнее. Ничего не ломается.')}</label>
     <label class="sw" style="display:flex"><input type="checkbox" id="rAds" ${D.ads?'checked':''}><span class="k"></span>Блокировать рекламу во всей сети ${qh('Встроенный список рекламных и следящих доменов (и geosite:category-ads-all, если он есть в базах). Для отдельного устройства можно включить или выключить в «Устройства».')}</label></div>
   </div></div>
  <div class="savebar" style="${changed?'':'display:none'}"><span>⚠️ <b>Вы изменили настройки, но они ещё не включены.</b> <span class="note">${rulesChangesText()}</span></span><span class="sp"></span><button class="btn" id="rulReset">Отменить</button><button class="btn pri" id="rulSave">Сохранить и включить</button></div>
  <div class="note" style="margin:-4px 4px 12px">Каталог: ${esc(R.catalog&&R.catalog.src||'встроенный')}${R.catalog&&R.catalog.updated?' · обновлён '+ago(R.catalog.updated):''}${R.catalog&&R.catalog.err?' · <span class="b">'+esc(R.catalog.err)+'</span>':''} · группа серверов: ${esc(R.balancer||'—')}</div>`;
}
async function rulesApply(body,what){
  try{const dry=await api('/api/rules',{...body,dry:true});
    if(dry.nochange){toast('Ничего не изменится');return false}
    if(!dry.ok){alert('Xray не примет это изменение:\n'+(dry.testError||''));return false}
    const diff=[...(dry.removed||[]).slice(0,5).map(x=>'− '+x.slice(0,140)),...(dry.added||[]).slice(0,5).map(x=>'+ '+x.slice(0,140))].join('\n');
    if(!confirm((what||'Применить правила?')+'\n\n'+diff+'\n\nПроверено Xray. Применить? Xray перезапустится, связь пропадёт на 5–10 с.'))return false;
    toast('Применяю…');await api('/api/rules',body);toast('Готово');return true}
  catch(e){alert(e.message);return false}}

document.addEventListener('change',e=>{if(page!=='sites'||!RD)return;
  if(e.target.closest('#catVPN')){if(catalogClick(e,RD.vpn,()=>{RD.vpn.forEach(id=>RD.novpn.delete(id));RD.dirty=true;renderPage()}))return}
  if(e.target.closest('#catNO')){if(catalogClick(e,RD.novpn,()=>{RD.novpn.forEach(id=>RD.vpn.delete(id));RD.dirty=true;renderPage()}))return}
  if(e.target.name==='rmode'){RD.mode=+e.target.value;RD.dirty=true;renderPage();return}
  if(e.target.id==='rQuic'){RD.quicOff=e.target.checked;RD.dirty=true;renderPage();return}
  if(e.target.id==='rAds'){RD.ads=e.target.checked;RD.dirty=true;renderPage();return}
});
document.addEventListener('click',async e=>{if(page!=='sites')return;const T=s=>e.target.closest(s);let t;
  if(t=T('#rulReset2')){e.preventDefault();RD=draftFrom(RUL.state);renderPage();return}
  if(t=T('#rulSave2')){e.preventDefault();t=null;document.getElementById('rulSave').click();return}
  if(t=T('#rulReset')){RD=draftFrom(RUL.state);renderPage();return}
  if(t=T('#rulSave')){const ok=await rulesApply({action:'save',mode:RD.mode,vpn:[...RD.vpn],novpn:[...RD.novpn],quicOff:RD.quicOff,ads:RD.ads},`«${MODES[RD.mode-1][1]}»: через VPN — ${RD.vpn.size} сервисов, без VPN — ${RD.novpn.size}.`);
    if(ok){RD.dirty=false;await loadRules();loadSites()}return}
  if(t=T('#rulAbsorb')){const ok=await rulesApply({action:'absorb'},'Записи из ваших правил, которые относятся к сервисам каталога, будут убраны из ваших правил и включены как сервисы.');if(ok){RD=null;await loadRules();loadSites()}return}
  if(t=T('[data-rignore]')){try{await api('/api/rules',{action:'ignore',host:t.dataset.rignore});loadRules()}catch(err){toast(err.message)}return}
});
</script>
<script>
"use strict";
// ================= РЕЖИМ УСТРОЙСТВА (п. 17, 23, 24, 25) =================
const DMODES=[['','Как у всей сети'],['1','Только выбранное через VPN'],['2','Всё через VPN, кроме российского'],['3','Абсолютно всё через VPN'],['novpn','Без VPN совсем']];
function devModeTag(ip){const dv=RUL&&RUL.state&&RUL.state.devices&&RUL.state.devices[ip];if(!dv)return '';const now=Date.now()/1000;let h='';
  if(dv.temp&&dv.until>now)h+=` <span class="tag a" title="временно, до ${tm(dv.until)}">⏱ ${esc((DMODES.find(m=>m[0]===dv.temp)||[])[1]||dv.temp)}</span>`;
  else if(dv.mode)h+=` <span class="tag">${esc((DMODES.find(m=>m[0]===dv.mode)||[])[1]||dv.mode)}</span>`;
  if((dv.kidsBlock||[]).length||dv.adult)h+=' <span class="tag m" title="детские ограничения">👶</span>';return h}
function untilEveningMin(){const d=new Date();const e=new Date();e.setHours(23,0,0,0);if(e<=d)e.setDate(e.getDate()+1);return Math.round((e-d)/60000)}
async function openDevRules(d){if(!RUL||RUL.error)await loadRules();if(!RUL||RUL.error){toast('Не удалось загрузить правила');return}
  if(!d.ip){toast('У устройства нет IP');return}
  const cur=Object.assign({mode:'',kidsBlock:[],kidsFrom:22,kidsTo:7,adult:false},(RUL.state.devices||{})[d.ip]||{});
  const kids=new Set(cur.kidsBlock||[]);const pinned=RUL.pinned&&RUL.pinned[d.ip];const now=Date.now()/1000;
  const kidSvcs=(RUL.services||[]).filter(s=>s.group!=='novpn');
  const box=document.createElement('div');box.className='devmodal';
  const draw=()=>{box.innerHTML=`<div class="card devcard" style="width:min(720px,calc(100vw - 24px));max-height:calc(100vh - 40px);overflow:auto">
   <h3>${esc(d.name||d.kname||'Устройство')}<span class="r note mono">${esc(d.ip)}</span></h3>
   ${pinned?'<div class="note g" style="margin-bottom:8px">✓ IP закреплён в Keenetic</div>':`<div class="banner" style="margin-bottom:10px">${RUL.pinnedErr?'Не удалось проверить, закреплён ли IP.':'IP этого устройства не закреплён — после перезагрузки он может смениться, и правила перестанут к нему относиться.'} Закрепите в Keenetic: «Список устройств» → устройство → «Постоянный IP-адрес». <a class="lnk" target="_blank" rel="noopener" href="${routerURL()}">Keenetic ↗</a></div>`}
   <div class="fg"><label>Режим интернета${qh('Свой режим для этого устройства. «Без VPN» — устройство всегда ходит через обычный интернет.')}<select class="in" id="drMode">${DMODES.map(([k,t])=>`<option value="${k}" ${cur.mode===k?'selected':''}>${t}${k===''?' (сейчас «'+MODES[(RUL.state.mode||1)-1][1]+'»)':''}</option>`).join('')}</select></label>
   <label>Реклама${qh('Блокировать рекламу только на этом устройстве (или, наоборот, не блокировать).')}<select class="in" id="drAds"><option value="" ${cur.ads==null?'selected':''}>как у всей сети</option><option value="1" ${cur.ads===true?'selected':''}>блокировать</option><option value="0" ${cur.ads===false?'selected':''}>не блокировать</option></select></label></div>
   <div style="margin:14px 0 6px"><b>Временно</b> ${cur.temp&&cur.until>now?`<span class="tag a">сейчас: ${esc((DMODES.find(m=>m[0]===cur.temp)||[])[1])} до ${tm(cur.until)}</span>`:''}</div>
   <div class="tools"><button class="btn" data-dt="3" data-min="60">На 1 час — всё через VPN</button><button class="btn" data-dt="3" data-min="${untilEveningMin()}">До вечера (23:00) — всё через VPN</button><button class="btn" data-dt="novpn" data-min="60">На 1 час — без VPN</button>${cur.temp?'<button class="btn" data-dt="" data-min="0">Снять</button>':''}</div>
   <div class="note" style="margin-top:4px">По окончании режим вернётся сам.</div>
   <details style="margin-top:14px" ${kids.size||cur.adult?'open':''}><summary><b>Детское устройство</b></summary>
    <label class="sw" style="display:flex;margin:10px 0"><input type="checkbox" id="drAdult" ${cur.adult?'checked':''}><span class="k"></span>Блокировать сайты для взрослых</label>
    <div class="tools"><span class="note">Блокировать отмеченные сервисы с</span><input class="in" type="number" id="drFrom" min="0" max="23" value="${cur.kidsFrom??22}" style="width:70px"><span class="note">до</span><input class="in" type="number" id="drTo" min="0" max="23" value="${cur.kidsTo??7}" style="width:70px"><span class="note">часов</span></div>
    <div id="drKids" style="margin-top:6px">${catalogHTML(kidSvcs,['popular','social','media','games','ai','work','other'],kids)}</div></details>
   <div class="tools" style="margin-top:14px;justify-content:flex-end"><button class="btn" data-x="cancel">Закрыть</button><button class="btn pri" data-x="save">Проверить и применить</button></div></div>`;fixIndeterminate(box)};
  draw();document.body.appendChild(box);
  const read=()=>({mode:box.querySelector('#drMode').value,name:d.name||d.kname||'',ads:box.querySelector('#drAds').value===''?null:box.querySelector('#drAds').value==='1',
    adult:box.querySelector('#drAdult').checked,kidsBlock:[...kids],kidsFrom:+box.querySelector('#drFrom').value,kidsTo:+box.querySelector('#drTo').value});
  box.addEventListener('change',e=>{if(e.target.closest('#drKids')){const keep=read();catalogClick(e,kids,()=>{});Object.assign(cur,keep,{kidsBlock:[...kids]});draw()}});
  box.addEventListener('click',async e=>{const x=e.target.closest('[data-x]'),t=e.target.closest('[data-dt]');
    if(e.target===box||(x&&x.dataset.x==='cancel')){box.remove();return}
    if(t){const ok=await rulesApply({action:'device',ip:d.ip,temp:t.dataset.dt,minutes:+t.dataset.min},'Временный режим для '+(d.name||d.ip)+'.');if(ok){box.remove();await loadRules();renderPage()}return}
    if(x&&x.dataset.x==='save'){const ok=await rulesApply({action:'device',ip:d.ip,dev:read()},'Режим устройства '+(d.name||d.ip)+'.');if(ok){box.remove();await loadRules();renderPage()}}});
}
</script>
