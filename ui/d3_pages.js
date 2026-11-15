<script>
"use strict";
// ================= САЙТЫ =================
let SITES=null, siteQ='', siteOpen=new Set();
async function loadSites(){try{SITES=await api('/api/sites');}catch(e){SITES={error:e.message}}if(page==='sites')renderPage()}
const KIND={vpn:['a','через VPN'],direct:['g','напрямую'],block:['b','блокировать'],other:['','особое']};
// regexp:… с \xHH — показать коротко и читаемо
function rxDecode(x){return x.replace(/\\x([0-9a-fA-F]{2})/g,(_,h)=>String.fromCharCode(parseInt(h,16)))}
function entryLabel(x){if(!x.startsWith('regexp:'))return esc(x.replace(/^domain:/,''));const d=rxDecode(x.slice(7));
  const words=(d.match(/[a-z0-9-]{3,}/gi)||[]).slice(0,4).join(', ');return `<span class="rx" title="${esc(d)}">шаблон${words?': '+esc(words)+(d.length>60?'…':''):''}</span>`}
function entryHTML(r,x,off){return `<span class="chip" style="${off?'opacity:.5;text-decoration:line-through':''}" title="${esc(x.startsWith('regexp:')?rxDecode(x):x)}">${entryLabel(x)}
  <b data-sop="${off?'enable':'disable'}" data-rule="${r.index}" data-val="${esc(x)}" title="${off?'Включить снова':'Временно отключить'}" style="cursor:pointer;margin-left:6px">${off?'↺':'⊘'}</b>
  <b data-sop="remove" data-rule="${r.index}" data-val="${esc(x)}" title="Удалить" style="cursor:pointer;margin-left:4px">✕</b></span>`}
function renderSites(){
  const el=$('#sitesOld')||$('#p-sites'); if(el.contains(document.activeElement)&&['INPUT','TEXTAREA','SELECT'].includes(document.activeElement.tagName))return;
  if(!SITES){el.innerHTML='<div class="card empty"><span class="spin"></span></div>';return}
  if(SITES.error){el.innerHTML=`<div class="card"><div class="banner">${esc(SITES.error)}</div></div>`;return}
  const q=siteQ.toLowerCase();
  const countries=(SITES.countries||[]).map(c=>`<option value="${esc(c.flag)}">${esc(c.flag)} (${c.n} серв.)</option>`).join('');
  const rulesAll=(SITES.rules||[]).filter(r=>!r.managed);
  const mkRule=r=>{
    const all=[...(r.domain||[]),...(r.ip||[])], off=[...(r.offDomain||[]),...(r.offIP||[])];
    const match=x=>!q||x.toLowerCase().includes(q);
    const shown=all.filter(match), shownOff=off.filter(match);
    if(q&&!shown.length&&!shownOff.length)return '';
    const [cls,kn]=KIND[r.kind]||KIND.other;
    const bind=(r.balancer||'').startsWith('xsel-');
    const target=r.balancer?(bind?'страна '+esc(r.balancer.slice(5).toUpperCase()):'группа '+esc(r.balancer)):esc(r.outbound||'');
    const open=siteOpen.has(r.index)||!!q, many=shown.length+shownOff.length>40&&!open;
    // 0.9: записи — группами по сервисам каталога, нераспознанное — «Свои сайты»
    const sv=r.svc||{},grp={};(many?shown.slice(0,40):shown).forEach(x=>{(grp[sv[x]||'']=grp[sv[x]||'']||[]).push([x,false])});if(!many)shownOff.forEach(x=>{(grp[sv[x]||'']=grp[sv[x]||'']||[]).push([x,true])});
    const names=Object.keys(grp).filter(k=>k).sort();const multi=names.length>0;
    const chips=[...names,''].filter(k=>grp[k]).map(k=>`${multi?`<span class="svcl">${k?esc(k):'Свои сайты'}</span>`:''}${grp[k].map(([x,o])=>entryHTML(r,x,o)).join('')}`).join(multi?'<span class="svcbr"></span>':'');
    return `<div class="card" style="padding:14px 16px"><div style="display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-bottom:${all.length+off.length?8:0}px">
      <span class="tag ${cls}">${bind?'🌍 '+kn+' · привязка':kn}</span><span class="note">${esc(r.description)} → ${target}</span>
      ${bind?`<button class="btn sm" data-unbind="${esc(r.balancer)}" style="margin-left:auto">Снять привязку</button>`:''}</div>
      ${all.length+off.length?`<div class="chips" style="flex-wrap:wrap">${chips}${many?`<button class="btn sm" data-sopen="${r.index}">показать все ${shown.length+shownOff.length}</button>`:''}</div>`:''}</div>`};
  const sysR=rulesAll.filter(r=>r.system).map(mkRule).filter(Boolean);
  const rules=rulesAll.filter(r=>!r.system).map(mkRule).join('')+(sysR.length?`<details class="card x-exp" style="padding:12px 16px" ${q?'open':''}><summary><b>Служебные правила (${sysR.length})</b> <span class="note">— локальные адреса, QUIC, вход XKeen, «всё остальное». Обычно трогать не нужно.</span></summary><div style="margin-top:10px">${sysR.join('')}</div></details>`:'');
  el.innerHTML=`<div class="grid g2">
   <div class="card"><h3>Проверить сайт</h3><div class="tools"><input class="in" id="stHost" placeholder="instagram.com или ссылка" style="flex:1;min-width:180px">
     <button class="btn" id="stRoute">Куда пойдёт</button><button class="btn pri" id="stDiag">Почему не открывается</button></div><div id="stOut" style="margin-top:10px"></div></div>
   <div class="card"><h3>Добавить сайты</h3><textarea class="in" id="stAdd" placeholder="Сайты, ссылки или IP — через пробел, запятую или с новой строки" style="min-height:70px"></textarea>
     <div class="tools" style="margin-top:8px"><select class="in" id="stTarget"><option value="vpn">через VPN</option><option value="direct">всегда напрямую</option><option value="block">блокировать</option><option value="bind">через страну…</option></select>
     <select class="in" id="stCountry" style="display:none">${countries}</select><button class="btn pri" id="stApply">Проверить и применить</button></div><div id="stAddOut" class="note" style="margin-top:8px"></div></div></div>
  <div class="card" style="padding:12px 16px"><div class="tools"><input class="in" id="stQ" placeholder="Поиск по всем правилам" value="${esc(siteQ)}" style="flex:1">
   <span class="note">${esc(SITES.file||'')} · правила проверяются сверху вниз — срабатывает первое подходящее; сервисы из «Правил» выше идут раньше этих · ⊘ — временно отключить, ✕ — удалить</span></div></div>
  ${rules||'<div class="card empty">Ничего не найдено</div>'}
  <div class="card"><h3>Резервные копии конфигов<span class="r">делаются перед каждым изменением, хранятся последние 20</span></h3>
   ${(SITES.backups||[]).slice(0,8).map(b=>`<div style="display:flex;gap:12px;align-items:center;padding:7px 0;border-top:1px solid var(--line)"><span class="mono n" style="white-space:nowrap">${esc(b.id.replace(/^(\d{4})(\d\d)(\d\d)-(\d\d)(\d\d)(\d\d).*/,'$3.$2 $4:$5'))}</span><span style="flex:1;min-width:0">${esc(b.what||'')}</span><button class="btn sm" data-restore="${esc(b.id)}">Вернуть</button></div>`).join('')||'<div class="note">Пока нет</div>'}</div>`;
  $('#stTarget').onchange=()=>{$('#stCountry').style.display=$('#stTarget').value==='bind'?'':'none'};
  let qt;$('#stQ').oninput=()=>{clearTimeout(qt);qt=setTimeout(()=>{siteQ=$('#stQ').value;const p=$('#stQ').selectionStart;renderSitesForce();const i=$('#stQ');i.focus();i.setSelectionRange(p,p)},250)};
}
function renderSitesForce(){const a=document.activeElement;a&&a.blur&&a.blur();renderSites()}

function stepsHTML(d){return `<table class="t">${(d.steps||[]).map(s=>`<tr><td>${s.ok?'<span class="g">✓</span>':'<span class="b">✕</span>'}</td><td><b>${esc(s.name)}</b><div class="note">${esc(s.info||'')}</div></td><td class="mono n">${s.ms?s.ms+' мс':''}</td></tr>`).join('')}</table>
  ${d.verdict?`<div class="banner" style="margin-top:10px">${esc(d.verdict)}</div>`:''}`}

// 0.9: правки сайтов копятся и применяются пачкой — один перезапуск Xray вместо десяти
let SITE_Q=[];
function siteOpText(b){const v=(b.values||[]).join(', ');const tg={vpn:'через VPN',direct:'напрямую',block:'блокировать'}[b.target]||b.target||'';
  return {addTo:`добавить в «${tg}»: ${v}`,add:`добавить в правило: ${v}`,remove:`удалить: ${v}`,disable:`временно отключить: ${v}`,enable:`включить: ${v}`,bind:`привязать к стране ${b.target}: ${v}`,unbind:`снять привязку к стране: ${v}`,ruleOff:'отключить правило',ruleOn:'включить правило'}[b.op]||b.op}
async function siteChange(body,confirmText){
  if(body.op==='restore'){try{await api('/api/sites',body);await loadSites();return true}catch(e){alert(e.message);return false}}
  SITE_Q.push({...body});toast('Добавлено в список изменений — нажмите «Проверить и применить» внизу');renderSiteBar();return true}
function renderSiteBar(){let b=$('#siteBar');if(!SITE_Q.length){if(b)b.remove();return}
  if(!b){b=document.createElement('div');b.id='siteBar';b.className='savebar';document.body.appendChild(b)}
  b.innerHTML=`<span>⚠️ <b>Изменения сайтов ещё не применены (${SITE_Q.length})</b><span class="note" style="display:block;max-height:4.5em;overflow:auto">${SITE_Q.map(x=>esc(siteOpText(x))).join('<br>')}</span></span><span class="sp"></span><button class="btn" id="siteQReset">Отменить</button><button class="btn pri" id="siteQApply">Проверить и применить</button>`;
  b.style.display=page==='sites'?'':'none'}
async function siteQApply(){const ops=SITE_Q.map(x=>({op:x.op,rule:x.rule,values:x.values,target:x.target}));
  try{const dry=await api('/api/sites',{ops,dry:true});
    if(dry.nochange){alert('Ничего не изменится');SITE_Q=[];renderSiteBar();return}
    if(!dry.ok){alert('Xray не примет эти изменения:\n'+(dry.testError||''));return}
    const diff=[...(dry.removed||[]).slice(0,6).map(x=>'− '+x.slice(0,140)),...(dry.added||[]).slice(0,6).map(x=>'+ '+x.slice(0,140))].join('\n');
    if(!confirm(SITE_Q.length+' изменений:\n'+SITE_Q.map(siteOpText).join('\n')+'\n\n'+diff+'\n\nПроверено Xray. Применить? Xray перезапустится один раз, связь пропадёт на 5–10 с.'))return;
    toast('Применяю…');await api('/api/sites',{ops});SITE_Q=[];renderSiteBar();toast('Готово');await loadSites();if(typeof loadRules==='function')loadRules()}
  catch(e){alert(e.message);await loadSites()}}
document.addEventListener('click',e=>{if(e.target.closest('#siteQApply')){siteQApply();return}if(e.target.closest('#siteQReset')){SITE_Q=[];renderSiteBar();toast('Изменения отменены')}});
window.addEventListener('beforeunload',e=>{if(SITE_Q.length){e.preventDefault();e.returnValue=''}});

document.addEventListener('click',async e=>{
  const T=s=>e.target.closest(s);let t;
  if(page!=='sites'&&page!=='tools'&&page!=='settings')return;
  if(t=T('[data-sopen]')){siteOpen.add(+t.dataset.sopen);renderSites();return}
  if(t=T('[data-sop]')){await siteChange({op:t.dataset.sop,rule:+t.dataset.rule,values:[t.dataset.val]});return}
  if(t=T('[data-unbind]')){await siteChange({op:'unbind',target:SITES.countries.find(c=>('xsel-'+[...c.flag].map(ch=>String.fromCharCode(ch.codePointAt(0)-0x1F1E6+97)).join(''))===t.dataset.unbind)?.flag||t.dataset.unbind.slice(5)});return}
  if(t=T('[data-restore]')){if(!confirm('Вернуть конфиги из этой копии? Xray перезапустится.'))return;try{await api('/api/sites',{op:'restore',target:t.dataset.restore});alert('Готово');loadSites()}catch(err){alert(err.message)}return}
  if(t=T('#stRoute')||T('#stDiag')){const h=$('#stHost').value.trim();if(!h)return;const out=$('#stOut');out.innerHTML='<span class="spin"></span> проверяю…';
    try{if(t.id==='stRoute'){const r=await api('/api/diag?route=1&host='+encodeURIComponent(h));const [cls,kn]=KIND[r.kind]||KIND.other;
      out.innerHTML=`<div><span class="tag ${cls}">${kn}</span> ${r.rule>=0?`правило №${r.rule+1}`:''} · <span class="note">${esc(r.why||'')}</span>${r.active?`<div class="note">сейчас через ${srvName(r.active,true)}</div>`:''}${r.disabled?`<div class="note">совпало бы с отключённой записью ${esc(r.disabled)}</div>`:''}<div class="note mono">${esc((r.ips||[]).slice(0,4).join(', '))}</div></div>`}
    else{const dg=await api('/api/diag?host='+encodeURIComponent(h));out.innerHTML=stepsHTML(dg)+`<div class="tools" style="margin-top:10px"><button class="btn pri" data-addvpn="${esc(h)}">Добавить в VPN</button><span class="note">сайт пойдёт через VPN-серверы</span></div>`}}catch(err){out.textContent=err.message}return}
  if(t=T('[data-addvpn]')){await siteChange({op:'addTo',target:'vpn',values:[t.dataset.addvpn]});return}
  if(t=T('#stApply')){const v=$('#stAdd').value.trim();if(!v)return;const tg=$('#stTarget').value;
    const ok=tg==='bind'?await siteChange({op:'bind',target:$('#stCountry').value,values:[v]}):await siteChange({op:'addTo',target:tg,values:[v]});
    if(ok){$('#stAdd').value='';$('#stAddOut').textContent='Готово'}return}
  // ---- обслуживание ----
  if(t=T('#subPaidSave')){const v=$('#subPaid').value;try{await api('/api/sub',{paidUntil:v?Math.round(new Date(v+'T23:59:00').getTime()/1000):0,action:'save'});toast('Запомнил');loadTools()}catch(err){toast(err.message)}return}
  if(t=T('#subSave')||T('#subPreview')||T('#subApply')){const body={url:$('#subUrl').value,ua:$('#subUA').value,auto:$('#subAuto').checked,everyH:+$('#subEvery').value,action:t.id==='subSave'?'save':t.id==='subPreview'?'preview':'apply'};
    if(body.action==='apply'&&!confirm('Заменить серверы в конфиге на серверы из подписки? Будет резервная копия, проверка Xray и откат при ошибке. Xray перезапустится.'))return;
    const out=$('#subOut');out.innerHTML='<span class="spin"></span>';try{const r=await api('/api/sub',body);out.innerHTML=planHTML(r.plan)||'Сохранено';loadTools()}catch(err){out.textContent=err.message;loadTools()}return}
  if(t=T('[data-rescheck]')){const n=t.dataset.rescheck,o=document.getElementById('rc_'+n);o.innerHTML='<span class="spin"></span>';
    try{const r=await api('/api/reserve',{check:n});o.innerHTML=r.ok?'<span class="g">✓ сервисы открываются</span>':'<span class="b">✕ '+esc(r.why||'не открываются')+'</span>'}catch(err){o.textContent=err.message}return}
  if(t=T('#resSave')){const ifaces=[...document.querySelectorAll('.resChk:checked')].map(x=>x.value);const out=$('#resOut');
    if(!confirm(ifaces.length?'Записать резерв в конфиг Xray? Xray перезапустится (5–10 с).':'Убрать резерв из конфига? Xray перезапустится.'))return;out.innerHTML='<span class="spin"></span>';
    try{await api('/api/reserve',{ifaces,auto:$('#resAuto').checked});out.textContent='Готово';loadTools()}catch(err){out.textContent=err.message}return}
  if(t=T('#geoUpd')){if(!confirm('Обновить базы geosite/geoip (xkeen -ug)? Займёт 1–5 минут, Xray перезапустится.'))return;const out=$('#geoOut');out.innerHTML='<span class="spin"></span> обновляю…';t.disabled=true;
    try{const r=await api('/api/maint',{action:'geo'});out.innerHTML=`<pre class="log" style="max-height:200px">${esc(r.output||'')}</pre>`;loadTools()}catch(err){out.textContent=err.message}t.disabled=false;return}
  if(t=T('#dnsNow')){t.disabled=true;t.textContent='проверяю…';await loadDNS(true);return}
  if(t=T('#snapBtn')){try{await api('/api/maint',{action:'snapshot'});loadTools()}catch(err){alert(err.message)}return}
  if(t=T('#updBtn')){const f=$('#updFile').files[0];if(!f){alert('Выберите файл xray-selector.tar.gz');return}runPanelUpdate({file:f});return}
  // ---- пароль ----
  if(t=T('#pwSave')||T('#pwOff')){const body={old:$('#pwOld')?.value||'',new:t.id==='pwOff'?'':$('#pwNew').value};
    if(t.id==='pwSave'&&body.new!==$('#pwNew2').value){alert('Пароли не совпадают');return}
    if(t.id==='pwOff'&&!confirm('Снять пароль? Панель снова будет открыта всем в домашней сети.'))return;
    try{await api('/api/password',body);PW=null;alert(body.new?'Пароль задан':'Пароль снят');renderSettings()}catch(err){alert(err.message)}return}
  if(t=T('#logoutBtn')){await api('/api/logout',{});location.href='/login';return}
});

function planHTML(p){if(!p)return'';const l=(a,n)=>a&&a.length?`<div class="note">${n} (${a.length}): ${a.slice(0,12).map(x=>esc(x)).join(', ')}${a.length>12?'…':''}</div>`:'';
  return `<div><b>${esc(p.file)}</b> · скачано ${esc(p.via||'')} · без изменений: ${p.same||0}</div>${l(p.added,'Новые')}${l(p.removed,'Убраны')}${l(p.changed,'Изменились')}${l(p.skipped,'Пропущены')}`}

// ================= ОБСЛУЖИВАНИЕ =================
let TOOLS=null;
function fmtLeftJS(sec){return sec<86400?Math.round(sec/3600)+' ч':Math.round(sec/86400)+' дн'}
async function loadTools(){try{const [m,s,r]=await Promise.all([api('/api/maint'),api('/api/sub'),api('/api/reserve')]);TOOLS={m,s,r}}catch(e){TOOLS={error:e.message}}if(page==='tools')renderTools()}
function renderTools(){
  const el=$('#p-tools'); if(el.contains(document.activeElement)&&['INPUT','TEXTAREA','SELECT'].includes(document.activeElement.tagName))return;
  if(!TOOLS){el.innerHTML='<div class="card empty"><span class="spin"></span></div>';return}
  if(TOOLS.error){el.innerHTML=`<div class="card"><div class="banner">${esc(TOOLS.error)}</div></div>`;return}
  const {m,s,r}=TOOLS; const now=Date.now()/1000;
  const used=(s.up||0)+(s.down||0), left=s.total?s.total-used:0;
  const effE=s.effExpire||0, exp=effE?Math.round((effE-now)/86400):null;
  el.innerHTML=`<div class="grid g2">
  <div class="card" data-st="sub"><h3>Подписка<span class="r">${s.checked?'проверена '+ago(s.checked):''}</span></h3>
   ${s.title?`<div style="font-weight:650;margin-bottom:6px">${esc(s.title)}</div>`:''}
   ${s.total||effE?`<div class="kv" style="margin-bottom:10px">${effE?`<div><span>Действует <span class="n">(${esc(s.effSrc)})</span></span><b class="${exp<3?'b':exp<7?'m':'g'}">${effE>now?'ещё '+fmtLeftJS(effE-now):'истекла'} · до ${new Date(effE*1000).toLocaleDateString('ru')}</b></div>`:''}${s.total?`<div><span>Трафик</span><b>${gb(used)} из ${gb(s.total)} · осталось ${gb(left)}</b><div class="prog" style="margin-top:4px"><i style="width:${Math.min(100,Math.round(used*100/s.total))}%;${used>s.total*.8?'background:var(--bad)':''}"></i></div></div>`:s.infLeft?`<div><span>Трафик (из названий)</span><b>осталось ${gb(s.infLeft)}</b></div>`:''}</div>`:''}
   ${s.support||s.webPage?`<div class="tools" style="margin-bottom:10px">${s.support?`<a class="btn sm" target="_blank" rel="noopener" href="${esc(s.support)}">💬 Написать в поддержку</a>`:''}${s.webPage?`<a class="btn sm" target="_blank" rel="noopener" href="${esc(s.webPage)}">Личный кабинет ↗</a>`:''}</div>`:''}
   ${(s.informers||[]).length?`<div class="note" style="margin-bottom:8px">Не серверы, а подсказки провайдера (в Xray не добавляются): ${s.informers.map(x=>'«'+esc(x)+'»').join(', ')}</div>`:''}
   ${!s.expire?`<div class="tools" style="margin-bottom:10px"><span class="note">Провайдер не сообщает срок. Оплачено до:</span><input class="in" type="date" id="subPaid" value="${s.paidUntil?new Date(s.paidUntil*1000).toISOString().slice(0,10):''}" style="max-width:170px"><button class="btn sm" id="subPaidSave">Запомнить</button></div>`:''}
   ${s.err?`<div class="banner" style="margin-bottom:8px">${esc(s.err)}</div>`:''}
   <div class="fg"><label class="full">Ссылка на подписку<input class="in" id="subUrl" value="${esc(s.url||'')}" placeholder="https://…"></label>
   <label>User-Agent<input class="in" id="subUA" value="${esc(s.ua||'')}"></label><label>Обновлять каждые, ч${s.updateH?` <span class="n">(провайдер советует ${s.updateH})</span>`:''}<input class="in" id="subEvery" type="number" min="1" value="${s.everyH||12}"></label>
   <label class="full"><span class="sw"><input type="checkbox" id="subAuto" ${s.auto?'checked':''}><span class="k"></span>Обновлять серверы автоматически (с проверкой и откатом)</span></label></div>
   <div class="tools" style="margin-top:10px"><button class="btn" id="subSave">Сохранить</button><button class="btn" id="subPreview">Что изменится</button><button class="btn pri" id="subApply">Обновить сейчас</button></div>
   <div id="subOut" style="margin-top:8px"></div>
   <div class="note" style="margin-top:6px">Серверы в ${'04_outbounds.json'} заменяются целиком (direct/block остаются). Первый раз нажмите «Что изменится» — если почти все серверы «изменились», значит, XKeen UI собирал их иначе; проверьте пару серверов после обновления.</div></div>

  <div class="card" data-st="reserve"><h3>Резерв через подключения Keenetic</h3>
   <div class="note" style="margin-bottom:8px">Если ни один сервер подписки не работает (провайдер режет всё), сторож по очереди проверит отмеченные подключения роутера — WireGuard, прокси, OpenVPN — и переключится на первое рабочее. Когда серверы оживут, вернётся на них.</div>
   ${(r.ifaces||[]).length?`<table class="t">${r.ifaces.map(i=>{const on=(r.configured||[]).some(c=>c.iface===i.name);return `<tr><td><label class="sw"><input type="checkbox" class="resChk" value="${esc(i.name)}" ${on?'checked':''}><span class="k"></span></label></td>
     <td><b>${esc(i.type)} · ${esc(i.id||i.name)}</b>${i.desc?` <span class="note">${esc(i.desc)}</span>`:''}<div class="note">${esc(i.name)} · ${i.up?'<span class="g">подключено</span>':'<span class="b">не подключено</span>'}${on?' · <span class="g">в резерве</span>':''}</div></td>
     <td><button class="btn sm" data-rescheck="${esc(i.name)}">Проверить</button><div class="note" id="rc_${esc(i.name)}"></div></td></tr>`}).join('')}</table>
   <label class="sw" style="margin-top:10px;display:flex"><input type="checkbox" id="resAuto" ${r.auto?'checked':''}><span class="k"></span>Подхватывать новые подключения сами (в спокойное время, с проверкой)</label>
   <div class="tools" style="margin-top:10px"><button class="btn pri" id="resSave">Сохранить резерв</button></div>`:'<div class="note">Подключений WireGuard / прокси / OpenVPN на роутере не найдено. Добавьте их в Keenetic: «Интернет» → «Другие подключения».</div>'}
   <div id="resOut" class="note" style="margin-top:8px"></div>
   <div class="note" style="margin-top:6px">Если загрузить в Keenetic новый файл в то же подключение (например, новый .conf в Wireguard0), резерв продолжит работать сам — привязка идёт к подключению, а не к файлу.</div></div>

  <div class="card x-exp" data-st="maint"><h3>Базы сайтов (geosite / geoip)<span class="r">${esc(m.xray||'')}</span></h3>
   <table class="t">${(m.dat||[]).map(d=>`<tr><td>${esc(d.name)}</td><td class="mono n">${(d.size/1048576).toFixed(1)} МБ</td><td class="note">${new Date(d.mod*1000).toLocaleDateString('ru')} · ${ago(d.mod)}</td></tr>`).join('')||'<tr><td class="n">не найдено в '+esc(m.assetDir||'')+'</td></tr>'}</table>
   <div class="tools" style="margin-top:10px"><button class="btn pri" id="geoUpd">Обновить базы</button></div><div id="geoOut" style="margin-top:8px"></div></div>

  <div class="card x-exp" data-st="maint"><h3>Самовосстановление</h3>
   <div class="note">Раз в 10 минут, пока Xray работает, программа запоминает конфиги как «рабочие». Если Xray перестанет запускаться из-за ошибки в конфиге, она сама вернёт рабочий набор.</div>
   <div style="margin:8px 0">Рабочий набор: <b>${esc(m.goodSnapshot||'ещё нет')}</b></div><button class="btn" id="snapBtn">Запомнить текущие сейчас</button>
   <h3 style="margin-top:16px">Резервные копии</h3>${(m.backups||[]).slice(0,6).map(b=>`<div style="display:flex;gap:12px;align-items:center;padding:7px 0;border-top:1px solid var(--line)"><span class="mono n" style="white-space:nowrap">${esc(b.id.replace(/^(\d{4})(\d\d)(\d\d)-(\d\d)(\d\d)(\d\d).*/,'$3.$2 $4:$5'))}</span><span style="flex:1;min-width:0">${esc(b.what||'')}</span><button class="btn sm" data-restore="${esc(b.id)}">Вернуть</button></div>`).join('')||'<div class="note">Пока нет</div>'}</div>

  <div class="card" data-st="maint"><h3>Обновление панели<span class="r">сейчас ${esc(m.selector)} · ${esc(m.arch)}</span></h3>
   <div class="note" style="margin-bottom:8px">Выберите архив xray-selector.tar.gz — панель сама возьмёт нужный файл, проверит его на роутере и перезапустится. SSH не нужен.</div>
   <div class="tools"><input type="file" id="updFile" accept=".gz,.tgz" class="in"><button class="btn pri" id="updBtn">Установить</button></div><div id="updOut" class="note" style="margin-top:8px"></div></div>
  </div>${maintExtra()}`;
  filterCards(el);
}

// ================= ТИПЫ УСТРОЙСТВ (значки) =================
const KP={ // контуры 24×24, линия 1.7
 pc:'<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/>',
 laptop:'<rect x="4" y="5" width="16" height="11" rx="1.5"/><path d="M2 19h20"/>',
 macbook:'<rect x="4" y="5" width="16" height="11" rx="1.5"/><path d="M2 19h20M10.5 5h3"/><circle cx="12" cy="10.5" r="1.6"/>',
 imac:'<rect x="3" y="3" width="18" height="13" rx="2"/><path d="M3 12.5h18M10 16l-1 5h6l-1-5"/>',
 iphone:'<rect x="7" y="2" width="10" height="20" rx="2.6"/><path d="M10.6 5h2.8"/>',
 android:'<rect x="6" y="2" width="12" height="20" rx="2"/><circle cx="12" cy="18.3" r=".9"/>',
 tablet:'<rect x="4" y="3" width="16" height="18" rx="2"/><circle cx="12" cy="18" r=".8"/>',
 watch:'<rect x="7" y="6" width="10" height="12" rx="3"/><path d="M9 6l.6-3h4.8L15 6M9 18l.6 3h4.8l.6-3M12 10v2.5l1.5 1"/>',
 tv:'<rect x="2" y="6" width="20" height="13" rx="2"/><path d="M8 22h8M8 2l4 4 4-4"/>',
 ps:'<path d="M6.5 8.5h11a4 4 0 0 1 3.9 4.9l-.9 3.6a2.4 2.4 0 0 1-4.2.9L14.6 16H9.4l-1.7 1.9a2.4 2.4 0 0 1-4.2-.9l-.9-3.6a4 4 0 0 1 3.9-4.9z"/><path d="M7.5 11v3M6 12.5h3"/><circle cx="15.8" cy="11.6" r=".8"/><circle cx="17.6" cy="13.4" r=".8"/>',
 xbox:'<circle cx="12" cy="12" r="9"/><path d="M7.5 6.8C9.7 8 11 10 12 12c1-2 2.3-4 4.5-5.2M7.3 17.6C8.6 15.4 10 13.8 12 12c2 1.8 3.4 3.4 4.7 5.6"/>',
 switch:'<rect x="2.5" y="5" width="5.5" height="14" rx="2.7"/><rect x="16" y="5" width="5.5" height="14" rx="2.7"/><rect x="8" y="5" width="8" height="14"/><circle cx="5.2" cy="9" r="1"/><circle cx="18.8" cy="15" r="1"/>',
 media:'<rect x="3" y="9" width="18" height="7" rx="2"/><path d="M7 19.5h10"/><circle cx="17" cy="12.5" r=".9"/><path d="M9 5.5c2-1.3 4-1.3 6 0"/>',
 speaker:'<rect x="6.5" y="3" width="11" height="18" rx="5"/><circle cx="12" cy="14" r="2.6"/><circle cx="12" cy="7.4" r=".9"/>',
 camera:'<path d="M3.5 6.5 16 3.5l1.8 6-12.5 3z"/><path d="M10.8 11.3l1.6 4.4M4 21v-6h5.5"/><path d="M18 6.5l3-.7"/>',
 intercom:'<rect x="6.5" y="2" width="11" height="20" rx="2"/><circle cx="12" cy="15.5" r="2.5"/><path d="M9.5 6h5M9.5 8.7h5"/>',
 printer:'<path d="M6 9V3h12v6"/><rect x="3" y="9" width="18" height="8" rx="2"/><rect x="7" y="14" width="10" height="7"/>',
 nas:'<rect x="4" y="3" width="16" height="7" rx="1.5"/><rect x="4" y="14" width="16" height="7" rx="1.5"/><circle cx="8" cy="6.5" r=".9"/><circle cx="8" cy="17.5" r=".9"/><path d="M12 10v4"/>',
 iot:'<path d="M3 11.2 12 4l9 7.2V20a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z"/><path d="M9.5 16.5c1.4-1.3 3.6-1.3 5 0M7.7 14.3c2.4-2.2 6.2-2.2 8.6 0"/>',
 router:'<rect x="3" y="13" width="18" height="7" rx="2"/><path d="M7 13 5.5 6.5M17 13l1.5-6.5"/><circle cx="7" cy="16.5" r=".8"/><circle cx="10" cy="16.5" r=".8"/>',
 car:'<path d="M5 15l1.6-5A2.2 2.2 0 0 1 8.7 8.5h6.6a2.2 2.2 0 0 1 2.1 1.5L19 15"/><rect x="3" y="15" width="18" height="4.5" rx="1.5"/><path d="M5.5 19.5V21M18.5 19.5V21"/><circle cx="7" cy="17.2" r=".8"/><circle cx="17" cy="17.2" r=".8"/>',
 vacuum:'<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="3"/><path d="M8 6.5h8"/>',
 ebook:'<rect x="5" y="2" width="14" height="20" rx="2"/><path d="M8 7h8M8 10h8M8 13h5"/>',
 other:'<circle cx="12" cy="12" r="9"/><path d="M9.6 9.4a2.5 2.5 0 1 1 3.4 2.4c-.7.3-1 .8-1 1.5v.7"/><circle cx="12" cy="17" r=".7"/>'};
const KINDS={pc:['pc','Компьютер'],laptop:['laptop','Ноутбук'],macbook:['macbook','MacBook'],imac:['imac','iMac / Mac mini'],iphone:['iphone','iPhone'],android:['android','Смартфон Android'],tablet:['tablet','Планшет / iPad'],watch:['watch','Часы'],
 tv:['tv','Телевизор'],ps:['ps','PlayStation'],xbox:['xbox','Xbox'],switch:['switch','Nintendo Switch'],media:['media','Медиаприставка (Apple TV, Shield, Android TV)'],speaker:['speaker','Умная колонка (Алиса, HomePod)'],
 camera:['camera','Камера'],intercom:['intercom','Домофон / звонок'],printer:['printer','Принтер'],nas:['nas','NAS / сервер'],iot:['iot','Умный дом (хабы, датчики, розетки)'],router:['router','Роутер / точка доступа'],car:['car','Автомобиль'],vacuum:['vacuum','Робот-пылесос'],ebook:['ebook','Электронная книга'],other:['other','Другое']};
function devIcon(k,size){return `<svg viewBox="0 0 24 24" width="${size||18}" height="${size||18}" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">${KP[k]||KP.other}</svg>`}
function openDevEditor(d){
  let sel=d.kind||'other';
  const box=document.createElement('div');box.className='devmodal';
  const draw=()=>{box.innerHTML=`<div class="card devcard"><h3>Устройство<span class="r note mono">${esc(d.ip||'')}${d.isMac?' · '+esc(d.id):''}</span></h3>
    <label class="note" style="display:block">Имя<input class="in" style="display:block;width:100%;margin-top:4px" id="dmName" value="${esc(d.name||'')}" placeholder="${esc(d.kname||'например: Папа · iPhone')}"></label>
    ${d.kname?`<div class="note" style="margin-top:4px">В Keenetic называется «${esc(d.kname)}» — если оставить имя пустым, будет оно</div>`:''}
    <div class="note" style="margin:12px 0 6px">Значок</div><div class="kgrid">${Object.keys(KINDS).map(k=>`<button class="kbtn${k===sel?' on':''}" data-k="${k}" title="${esc(KINDS[k][1])}">${devIcon(k,26)}<span>${esc(KINDS[k][1].split(' (')[0])}</span></button>`).join('')}</div>
    <div class="tools" style="margin-top:14px;justify-content:flex-end"><button class="btn" data-x="cancel">Отмена</button><button class="btn pri" data-x="save">Сохранить</button></div></div>`};
  draw();document.body.appendChild(box);setTimeout(()=>box.querySelector('#dmName').focus(),30);
  box.addEventListener('click',async e=>{const k=e.target.closest('[data-k]');if(k){const nm=box.querySelector('#dmName').value;sel=k.dataset.k;draw();box.querySelector('#dmName').value=nm;return}
    const x=e.target.closest('[data-x]');if(e.target===box||(x&&x.dataset.x==='cancel')){box.remove();return}
    if(x&&x.dataset.x==='save'){await api('/api/devices',{id:d.id,name:box.querySelector('#dmName').value,kind:sel});box.remove();loadDevices()}});
}

// ================= ВЫБОР СЕРВЕРОВ ДЛЯ СТОРОЖА =================
async function openServerPicker(gtag){
  const w=wg(gtag),dead=new Set(S.dead||[]),inList=new Set(w.servers||[]);
  let sc24={};try{const st=await api('/api/state?win=86400');sc24=st.scores||{}}catch(e){}
  const tried=w.tried||{},now=Date.now()/1000;
  const rows=S.servers.filter(s=>!inList.has(s.tag)&&!dead.has(s.tag)).map(s=>{const st=S.stats&&S.stats[s.tag],w24=st&&st.w&&st.w.w24;
    const up=w24&&w24.n?Math.round((w24.n-w24.fail)*100/w24.n):null,ping=w24&&w24.avg||(S.results[s.tag]&&S.results[s.tag].delay)||0;
    return {tag:s.tag,net:s.network||'',sc:sc24[s.tag],up,ping,rej:tried[s.tag]&&now-tried[s.tag]<86400}});
  let sortBy='score',q='',sel=new Set();
  const box=document.createElement('div');box.className='devmodal';
  const order=l=>l.slice().sort((a,b)=>sortBy==='ping'?((a.ping||1e9)-(b.ping||1e9)):sortBy==='name'?a.tag.localeCompare(b.tag):(((b.sc&&b.sc.score)??-1)-((a.sc&&a.sc.score)??-1))||((b.up??-1)-(a.up??-1))||((a.ping||1e9)-(b.ping||1e9)));
  const draw=()=>{const list=order(rows.filter(r=>!q||r.tag.toLowerCase().includes(q)));
    box.innerHTML=`<div class="card devcard" style="width:min(760px,100%)"><h3>Добавить серверы в сторож · ${esc(gtag)}<span class="r note">${rows.length} доступно</span></h3>
     <div class="tools" style="margin-bottom:8px"><input class="in" id="pkQ" placeholder="Поиск: страна, протокол…" value="${esc(q)}" style="flex:1"><div class="seg">${[['score','по баллам'],['ping','по пингу'],['name','по имени']].map(([k,t])=>`<button data-sort="${k}" class="${sortBy===k?'on':''}">${t}</button>`).join('')}</div></div>
     <div style="max-height:52vh;overflow:auto"><table class="t"><thead><tr><th></th><th>Сервер</th><th>Баллы за сутки</th><th>Доступность</th><th>Пинг</th></tr></thead>${list.map(r=>`<tr data-pk="${esc(r.tag)}" style="cursor:pointer;${r.rej?'opacity:.55':''}"><td style="width:28px"><input type="checkbox" ${sel.has(r.tag)?'checked':''} tabindex="-1" style="pointer-events:none"></td>
       <td><b>${srvName(r.tag,true)}</b>${r.rej?'<div class="note b">отбракован сторожем за последние сутки</div>':r.sc&&r.sc.n?'<div class="note">уже проверялся сторожем</div>':''}</td>
       <td>${r.sc&&r.sc.n?scoreHTML(r.sc):'<span class="n">нет баллов</span>'}</td>
       <td class="mono ${r.up==null?'n':r.up>=90?'g':r.up>=60?'m':'b'}">${r.up==null?'—':r.up+'%'}</td><td class="mono n">${r.ping?r.ping+' мс':'—'}</td></tr>`).join('')||'<tr><td class="empty">Ничего не найдено</td></tr>'}</table></div>
     <div class="note" style="margin:6px 0">Баллы — по проверкам сторожа за сутки (если сервер в нём был). % и пинг — фоновая проверка всех серверов за сутки.</div>
     <div class="tools" style="justify-content:flex-end">${w.auto?`<select class="in" id="pkDest"><option value="cand">🧪 на проверку (кандидатами)</option><option value="main">📌 сразу в основные</option></select>`:''}
      <button class="btn" data-x="cancel">Отмена</button><button class="btn pri" data-x="add" ${sel.size?'':'disabled'}>Добавить ${sel.size||''}</button></div></div>`;
    const qi=box.querySelector('#pkQ');qi.oninput=()=>{q=qi.value.toLowerCase();const p=qi.selectionStart;draw();const n=box.querySelector('#pkQ');n.focus();n.setSelectionRange(p,p)}};
  draw();document.body.appendChild(box);
  box.addEventListener('click',async e=>{const r=e.target.closest('[data-pk]');if(r){const t=r.dataset.pk;sel.has(t)?sel.delete(t):sel.add(t);const st=box.querySelector('.devcard div[style*="overflow"]').scrollTop;draw();box.querySelector('.devcard div[style*="overflow"]').scrollTop=st;return}
    const so=e.target.closest('[data-sort]');if(so){sortBy=so.dataset.sort;draw();return}
    const x=e.target.closest('[data-x]');if(e.target===box||(x&&x.dataset.x==='cancel')){box.remove();return}
    if(x&&x.dataset.x==='add'&&sel.size){const list=[...sel];const dest=box.querySelector('#pkDest')?.value||'main';
      const ok=dest==='cand'?await wpost({group:gtag,addCand:list}):await wpost({group:gtag,servers:[...w.servers,...list]});
      if(ok){box.remove();toast(`Добавлено: ${list.length}`)}}});
}

// ================= ПОДКЛЮЧЕНИЯ KEENETIC В СТОРОЖЕ =================
var TUN=null;
async function loadTunnels(){try{TUN=await api('/api/tunnels')}catch(e){}if(page==='watch')renderPage()}
loadTunnels();setInterval(()=>{if(['watch','tools','home','net'].includes(page))loadTunnels()},30000);
function tunnelCard(){
  if(!TUN||!(TUN.tunnels||[]).length)return '';
  const tg=((W.settings||{}).targets||[]).slice(0,8);
  const rows=TUN.tunnels.map(r=>{const x=r.iface,h=r.health,sc=r.score,act=TUN.active===x.tag;
    const chips=tg.map(t=>{const q=h&&h.targets&&h.targets[t.name];const sh=SVC_SHORT[t.name]||t.name;return !q?`<span class="ck w">${esc(sh)}</span>`:q.ok?`<span class="ck" title="${esc(t.name)}: ${q.ms} мс">${esc(sh)} ${q.ms}</span>`:`<span class="ck x" title="${esc(t.name)}: ${esc(q.err||'')}">${esc(sh)} ✕</span>`}).join('');
    const state=!h||!h.t?'<span class="n">ещё не проверялось</span>':h.ok?`<span class="g">● здорово</span>${h.okSince?' <span class="n">'+dur(Date.now()/1000-h.okSince)+'</span>':''}`:`<span class="b">● ${esc(h.reason&&h.reason.includes('не активно')?'не подключено в Keenetic':'сбоев подряд: '+h.consecFail)}</span>`;
    const spd=!h||!h.speedT?'<span class="n">—</span>':h.speedErr?'<span class="b">✕</span>':`<span class="${h.speedKbps<1500?'b':h.speedKbps<3000?'m':'g'}">${mbps(h.speedKbps)}</span>`;
    return `<tr class="${act?'act':''}"><td class="c-name"><div style="font-weight:600;white-space:nowrap">${esc(x.type)} · ${esc(x.id||x.name)} ${act?'<span class="tag a">сейчас через него</span>':''} ${r.inReserve?'<span class="tag g">в резерве</span>':''}</div><div style="font-size:12px">${x.desc?esc(x.desc)+' · ':''}${state}</div></td>
      <td class="c-chk"><div class="chk">${chips}</div></td><td class="c-spd mono">${spd}<div class="n" style="font-size:11px">Мбит/с</div></td><td class="c-sc">${scoreHTML(sc)}</td></tr>`}).join('');
  return `<div class="card"><h3>🛟 Подключения Keenetic<span class="r">${TUN.off?'проверка выключена':'каждые '+(TUN.every||60)+' с'} · <button class="btn sm" data-tunall="1">⇩ Взять все в резерв</button> <button class="btn sm" data-tuncheck="1">⚡ Проверить сейчас</button></span></h3>
   <table class="t wt"><thead><tr><th>Подключение</th><th>Сервисы</th><th>Скорость</th><th>Баллы</th></tr></thead><tbody>${rows}</tbody></table>
   <div class="note" style="margin-top:8px">Проверяются те же сервисы и скорость, что у серверов подписки, — видно, на что можно рассчитывать, если провайдер срежет все серверы. Какие подключения брать в резерв — в «Обслуживании»; при аварии сторож возьмёт здоровое с лучшими баллами.</div></div>`}

// ================= ОБРЫВЫ ИНТЕРНЕТА =================
var PULSE=null;
async function loadPulse(){try{PULSE=await api('/api/pulse')}catch(e){}}
loadPulse();setInterval(loadPulse,30000);
function pulseCard(){const p=PULSE;if(!p)return '';const l=(p.outages||[]).slice().reverse();
  return `<div class="card"><h3>Обрывы интернета у роутера<span class="r">проверка каждые ${p.every||5} с · ${p.checks||0} проверок</span></h3>
   ${l.length?`<table class="t">${l.slice(0,30).map(o=>`<tr><td class="mono">${new Date(o.from*1000).toLocaleString('ru')}</td><td><b class="${(o.to?o.to-o.from:9e9)>60?'b':'m'}">${o.to?fmtS(o.to-o.from):'идёт сейчас'}</b></td><td class="note">${esc(o.iface||'')}</td></tr>`).join('')}</table>`:'<div class="note">Обрывов не было. Если игры или звонки всё равно рвутся — дело не в канале роутера, а в Wi-Fi, самом устройстве или VPN на нём.</div>'}
   <div class="note" style="margin-top:6px">Короткие пропадания (5–60 с), которых не видно в ежеминутной проверке, — именно из-за них игры пишут «нет соединения». Подробности — «Журнал» → «Роутер (журнал Keenetic)».</div></div>`}
function fmtS(s){return s<60?s+' с':Math.floor(s/60)+' мин '+(s%60)+' с'}

// ================= DNS =================
var DNSR=null;
async function loadDNS(now){try{DNSR=await api('/api/dns'+(now?'?now=1':''))}catch(e){}if(page==='tools')renderTools();if(page==='home'||page==='net')renderPage()}
loadDNS();setInterval(()=>loadDNS(),60000);
function dnsRow(r,main){const st=r.ok?'<span class="g">✓ отвечает</span>':r.block?'<span class="b">✕ блокирует провайдер</span> <span class="note">(через VPN отвечает)</span>':'<span class="b">✕ '+esc(r.err||'не отвечает')+'</span>';
  const h=(DNSR&&DNSR.hist&&DNSR.hist[main?'router':r.addr])||[];const bad=h.filter(p=>!p.ok).length;
  const S9=(DNSR&&DNSR.stats&&DNSR.stats[main?'router':r.addr])||null;
  const vcls=!S9?'':S9.rank===0?'g':S9.rank>=5?'b':S9.rank===3?'':'m';
  const pct=x=>x>=99.95?'100':x.toFixed(1).replace('.',',');
  const st9=S9&&S9.n24?`<div class="note" style="margin-top:3px">доступность ${pct(S9.up24)} % за сутки${S9.days>1?' · '+pct(S9.up7)+' % за '+S9.days+' дн':''}${S9.eps?` · ${S9.eps} сбо${S9.eps===1?'й':S9.eps<5?'я':'ев'} за сутки${S9.longMin?', самый долгий '+S9.longMin+' мин':''}`:' · без сбоев'}${S9.med?` · обычно ${S9.med} мс${S9.p90>S9.med*1.5?' (бывает до '+S9.p90+')':''}`:''}${S9.cache?' (ответ из памяти роутера)':''}${S9.block7?` · провайдер блокировал ${S9.block7} раз`:''}${S9.down7?` · сервер не отвечал ${S9.down7} раз`:''}${(S9.badHrs||[]).length?' · чаще сбоит в '+S9.badHrs.map(h=>h+' ч').join(', '):''}</div>`:'';
  return `<tr><td><b>${esc(r.name||r.addr)}</b>${S9&&S9.verdict?` <span class="tag ${vcls}">${esc(S9.verdict)}</span>`:''}${r.fastest?' <span class="tag g">быстрее всех</span>':''}<div class="note">${esc(r.kind)}${r.local?' · через '+esc(r.local):''}${main?'':' · '+esc(r.addr)}</div>${st9}</td><td>${st}${h.length?`<div class="bar" style="height:8px;margin-top:4px;min-width:160px" title="24 часа">${ispBar(h)}</div>`:''}</td><td class="mono n">${r.ok&&r.ms?r.ms+' мс':''}${bad?`<div class="m" style="font-size:11px">${bad} сбоев/24ч</div>`:''}</td></tr>`}
function dnsCard(){const d=DNSR;
  if(!d||d.pending)return `<div class="card"><h3>DNS<span class="r"><button class="btn sm" id="dnsNow">Проверить</button></span></h3><div class="note">Первая проверка — через пару минут после запуска.</div></div>`;
  const cls=d.level==='ok'?'g':d.level==='warn'?'m':'b';
  return `<div class="card"><h3>DNS — через что устройства находят сайты<span class="r">${ago(d.t)} · <button class="btn sm" id="dnsNow">Проверить сейчас</button></span></h3>
   <div class="${d.level==='ok'?'note':'banner'}" style="margin-bottom:8px"><b class="${cls}">${esc(d.verdict)}</b></div>
   <table class="t">${dnsRow(d.router,true)}${(()=>{const l=(d.list||[]).map(x=>({...x}));const ok=l.filter(x=>x.ok&&x.ms);if(ok.length>1){ok.sort((a,b)=>a.ms-b.ms);ok[0].fastest=true}
     const rk=x=>{const s9=d.stats&&d.stats[x.addr];return s9?s9.rank*1e6+(s9.med||9e5):5e6};l.sort((a,b)=>rk(a)-rk(b));return l.map(r=>dnsRow(r)).join('')})()||'<tr><td class="n" colspan=3>Не удалось прочитать список DNS-серверов из Keenetic</td></tr>'}</table>
   <div class="note" style="margin-top:6px">Серверы отсортированы от лучшего: сверху — те, что стоит поставить основными. Порядок DNS меняется в Keenetic: «Интернет → DNS-серверы» (<a class="lnk" target="_blank" rel="noopener" href="${routerURL()}">Keenetic ↗</a>). Сбои, когда пропадал весь интернет, серверам не засчитываются. Проверка — запрос ${esc(d.testName||'')} к каждому серверу из настроек Keenetic напрямую, как ходит сам роутер. Раз в 5 минут; если DNS роутера не отвечает 10+ минут — придёт сообщение.</div></div>`}

// ================= ПАРОЛЬ (в настройках) =================
let PW=null;
async function passwordCard(){/* data-st=pw выставляется на обёртке */if(PW===null){try{PW=await api('/api/password')}catch(e){PW={set:false}}}
  return `<div class="card"><h3>Пароль на панель<span class="r">${PW.set?'<span class="g">задан</span>':'<span class="n">не задан</span>'}</span></h3>
   ${PW.set?'':'<div class="note" style="margin-bottom:10px">Пароль необязателен. Без него панель открывается любому устройству в домашней сети. Если сеть только ваша и семьи, можно не задавать.</div>'}
   <div class="fg">${PW.set?'<label>Текущий пароль<input class="in" id="pwOld" type="password" autocomplete="current-password"></label>':''}
   <label>Новый пароль<input class="in" id="pwNew" type="password" autocomplete="new-password"></label><label>Ещё раз<input class="in" id="pwNew2" type="password" autocomplete="new-password"></label></div>
   <div class="tools" style="margin-top:10px"><button class="btn pri" id="pwSave">${PW.set?'Сменить':'Задать пароль'}</button>${PW.set?'<button class="btn" id="pwOff">Снять пароль</button><button class="btn" id="logoutBtn">Выйти</button>':''}</div>
   <div class="note" style="margin-top:8px">Вход запоминается на 30 дней. Забыли пароль — по SSH: <code>xray-selector -reset-password</code> и перезапуск.</div></div>`}
const _origRS=renderSettings;
renderSettings=function(){const el=$('#p-settings');if(el.contains(document.activeElement)&&['INPUT','TEXTAREA','SELECT'].includes(document.activeElement.tagName))return;
  _origRS();if(!$('#pwCard')){const d=document.createElement('div');d.id='pwCard';d.dataset.st='pw';el.prepend(d)}filterCards(el);
  passwordCard().then(h=>{const c=$('#pwCard');if(c&&!c.contains(document.activeElement))c.innerHTML=h})};
</script>
