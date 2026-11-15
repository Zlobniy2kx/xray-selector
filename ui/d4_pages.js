<script>
"use strict";
// ================= 2.3: режим интерфейса =================
const isExpert=()=>store.get('uiMode','simple')==='expert';
const qh=t=>`<span class="qhelp" title="${esc(t)}">?</span>`;

// ================= СЕТЬ =================
function renderNet(){const el=$('#p-net');
  el.innerHTML=`${homeProviders()}${typeof pulseCard==='function'?pulseCard():''}${typeof dnsCard==='function'?dnsCard():''}`;
}

// ================= ОБСЛУЖИВАНИЕ: дополнения 2.3 =================
function maintExtra(){const a=ABOUT||{};const g=a.guard||{};
  const lv=['<span class="g">спокойно</span>','<span class="m">занят — фоновые проверки реже</span>','<span class="b">перегружен — только самое нужное</span>'][g.level||0];
  return `<div class="grid g2">
  ${a.xkeenUI?`<div class="card" data-st="maint"><h3>XKeen UI</h3><div class="row"><span>Найдена XKeen UI</span><a class="btn sm" target="_blank" rel="noopener" href="${location.protocol}//${location.hostname}:${a.xkeenUIPort||1000}/">открыть ↗</a></div>
    <div class="banner" style="margin-top:8px">Не меняйте серверы и правила в двух панелях одновременно — одна перепишет изменения другой.</div></div>`:''}
  <div class="card x-exp" data-st="maint"><h3>Нагрузка роутера ${qh('Если роутер занят (нагрузка выше двух на ядро), панель реже проверяет серверы и откладывает полную проверку. Большие базы сайтов не загружаются, если мало свободной памяти.')}</h3>
    <div class="kv"><div><span>Сейчас</span><b>${lv}</b></div><div><span>Нагрузка</span><b class="mono">${g.load1!=null?g.load1.toFixed(2):'—'} · ядер ${g.cores||'—'}</b></div>
    <div><span>Свободно памяти</span><b class="mono">${g.availMB>=0?g.availMB+' МБ':'—'}</b></div><div><span>Притормаживали</span><b class="mono">${g.slowed||0} раз</b></div></div>
    ${g.last?`<div class="note" style="margin-top:6px">${esc(g.last)}</div>`:''}</div>
  ${isExpert()?`<div class="card" data-st="maint"><h3>Конфиги Xray (JSON) <span class="tag m">экспертный режим</span></h3>
    <div class="note" style="margin-bottom:8px">Правка файлов в ${esc('/opt/etc/xray/configs')}. Перед записью — проверка <code>xray -test</code>, резервная копия, перезапуск; если Xray не поднимется за 30 с — откат.</div>
    <div class="tools"><button class="btn" id="xcfgOpen">Открыть редактор</button></div></div>`:''}
  <div class="card" data-st="maint"><h3>Новые версии ${qh('Раз в сутки панель смотрит последнюю версию на GitHub — сначала через рабочий VPN-сервер, если не вышло — напрямую.')}</h3>${updBlock()}</div>
  <div class="card" data-st="maint"><h3>О программе</h3>
    <div class="kv"><div><span>Название</span><b>${esc(a.name||'xray-selector')}</b></div><div><span>Версия</span><b class="mono">${esc(a.version||(S&&S.version)||'')}</b></div><div><span>Автор</span><b>${esc(a.author||'Злобыч')}</b></div></div>
    <div class="note" style="margin-top:8px">${esc(a.desc||'')}</div>
    ${isExpert()&&!a.demo?`<div class="note" style="margin-top:8px">Учебный режим — копия панели на порту 9092, где можно пробовать что угодно, не трогая роутер. Запуск по SSH: <code>xray-selector -demo &amp;</code>, удаление: <code>xray-selector -demo-remove</code>.</div>`:''}
    <div class="tools" style="margin-top:8px">${a.projectURL?`<a class="btn sm" target="_blank" rel="noopener" href="${esc(a.projectURL)}">Страница проекта ↗</a>`:''}${a.supportURL?`<a class="lnk" target="_blank" rel="noopener" href="${esc(a.supportURL)}" style="font-size:13px">♥ Сказать спасибо разработчику</a>`:''}</div></div>
  </div>`;
}

let UPD=null;
async function loadUpd(){try{UPD=await api('/api/update')}catch(e){}}
function updBlock(){const u=UPD;if(!u)return '<div class="note">…</div>';
  if(!u.repo)return '<div class="note">Проверка обновлений включится, когда программа будет опубликована на GitHub. Пока обновляйте архивом выше.</div>';
  return `<div class="kv"><div><span>Сейчас</span><b class="mono">${esc(u.current)}</b></div><div><span>На GitHub</span><b class="mono ${u.newer?'g':''}">${esc(u.latest||'—')}</b></div></div>
   <div class="note" style="margin-top:6px">${u.checked?'проверено '+ago(u.checked)+(u.via?' · '+esc(u.via):''):'ещё не проверялось'}${u.err?' · <span class="b">'+esc(u.err)+'</span>':''}</div>
   ${u.newer&&u.notes?`<div class="note" style="white-space:pre-wrap;margin-top:6px;max-height:140px;overflow:auto">${esc(u.notes)}</div>`:''}
   <div class="tools" style="margin-top:8px"><button class="btn" id="updCheck">Проверить</button>${u.newer&&u.canInstall?`<button class="btn pri" id="updInstall">Установить ${esc(u.latest)}</button>`:''}${u.page?`<a class="lnk" target="_blank" rel="noopener" href="${esc(u.page)}">что нового ↗</a>`:''}</div>`}
// ================= Режим интерфейса (в Настройках → Основные) =================
const _origRS2=renderSettings;
renderSettings=function(){const el=$('#p-settings');if(el.contains(document.activeElement)&&['INPUT','TEXTAREA','SELECT'].includes(document.activeElement.tagName))return;
  _origRS2();let c=$('#modeCard');if(!c){c=document.createElement('div');c.id='modeCard';c.dataset.st='main';const pw=$('#pwCard');pw?pw.after(c):el.prepend(c)}
  c.innerHTML=`<div class="card"><h3>Режим интерфейса</h3><div class="tools"><div class="seg">${[['simple','Простой'],['expert','Экспертный']].map(([k,t])=>`<button data-uimode="${k}" class="${store.get('uiMode','simple')===k?'on':''}">${t}</button>`).join('')}</div></div>
   <div class="tools" style="margin-top:10px"><button class="btn" id="wzAgain">🧭 Пройти мастер заново</button></div>
   <div class="note" style="margin-top:8px">Простой режим прячет тонкие настройки сторожа, технические журналы и служебные карточки. Экспертный показывает всё, плюс правку JSON-конфигов Xray. Запоминается на этом устройстве.</div></div>`;
  filterCards(el)};

// ================= Редактор JSON-конфигов =================
async function openXcfg(){let l;try{l=await api('/api/xcfg')}catch(e){toast(e.message);return}
  const box=document.createElement('div');box.className='devmodal';
  box.innerHTML=`<div class="card" style="width:min(1000px,calc(100vw - 24px));max-height:calc(100vh - 40px);overflow:auto">
   <h3>Конфиги Xray<span class="r"><button class="btn icon" data-x="close">✕</button></span></h3>
   <div class="tools"><select class="in" id="xcfgName">${l.files.map(f=>`<option>${esc(f.name)}</option>`).join('')}</select><span class="sp"></span>
   <button class="btn" data-x="check">Проверить</button><button class="btn pri" data-x="save">Применить</button></div>
   <textarea class="in xcfg" id="xcfgText" spellcheck="false" style="margin-top:10px"></textarea><div id="xcfgOut" class="note" style="margin-top:6px"></div></div>`;
  document.body.appendChild(box);let orig='';
  const load=async()=>{const n=$('#xcfgName').value;try{const r=await api('/api/xcfg?name='+encodeURIComponent(n));orig=r.content;$('#xcfgText').value=r.content;$('#xcfgOut').textContent=''}catch(e){$('#xcfgOut').textContent=e.message}};
  $('#xcfgName').onchange=load;await load();
  box.onclick=async e=>{const b=e.target.closest('[data-x]');if(e.target===box||(b&&b.dataset.x==='close')){if($('#xcfgText').value!==orig&&!confirm('Закрыть без сохранения?'))return;box.remove();return}
    if(!b)return;const name=$('#xcfgName').value,content=$('#xcfgText').value,out=$('#xcfgOut');
    if(b.dataset.x==='check'){out.textContent='проверяю…';try{await api('/api/xcfg',{name,content,check:true});out.innerHTML='<span class="g">✓ Xray принимает этот конфиг</span>'}catch(err){out.innerHTML='<span class="b">'+esc(err.message)+'</span>'}}
    if(b.dataset.x==='save'){if(content===orig){toast('Изменений нет');return}if(!confirm('Применить '+name+'? Xray перезапустится, связь пропадёт на 5–10 секунд.'))return;
      b.disabled=true;out.textContent='применяю…';try{await api('/api/xcfg',{name,content});orig=content;out.innerHTML='<span class="g">✓ Применено</span>'}catch(err){out.innerHTML='<span class="b">'+esc(err.message)+'</span>'}b.disabled=false}};
}

document.addEventListener('click',async e=>{const t=e.target.closest('[data-uimode],#xcfgOpen,#updCheck,#updInstall,#wzAgain');if(!t)return;
  if(t.id==='wzAgain'){openWizard();return}
  if(t.id==='xcfgOpen'){openXcfg();return}
  if(t.id==='updCheck'){t.disabled=true;t.textContent='проверяю…';try{UPD=await api('/api/update',{action:'check'})}catch(err){toast(err.message)}renderPage();return}
  if(t.id==='updInstall'){if(!confirm('Скачать и установить '+UPD.latest+'? Панель перезапустится.'))return;runPanelUpdate({github:true});return}
  store.set('uiMode',t.dataset.uimode);applyTheme();renderPage();toast(t.dataset.uimode==='expert'?'Экспертный режим':'Простой режим')});

// ================= Telegram: состояние доставки =================
async function tgStatus(sent){const el=$('#tgStat');if(!el)return;let r={};try{r=await api('/api/telegram')}catch(e){}
  const st=W&&W.settings||{};let h=[];
  if(sent){if(sent.admin)h.push('<span class="g">✓ личка: доставлено</span>');if(sent.group)h.push('<span class="g">✓ группа: доставлено</span>');if(sent.adminErr)h.push('<span class="b">✕ личка: '+esc(sent.adminErr)+'</span>');if(sent.groupErr)h.push('<span class="b">✕ группа: '+esc(sent.groupErr)+'</span>')}
  if(st.tgToken&&!r.admin) h.push('<span class="m">Личка не подключена — в Telegram придут только аварии. Нажмите «Подключить мою личку».</span>');
  if(r.privateAsGroup) h.push('Подключён личный чат: всё приходит в него.');
  if(r.okT) h.push('последняя отправка: '+ago(r.okT));
  if(r.lastErr&&r.errT>(r.okT||0)) h.push('<span class="b">последняя ошибка ('+ago(r.errT)+'): '+esc(r.lastErr)+'</span>');
  el.innerHTML=h.join(' · ')}
const _origRS3=renderSettings;
renderSettings=function(){_origRS3();if(pageFilter==='tg'&&$('#tgStat')&&!$('#tgStat').innerHTML)tgStatus()};
// ================= ОБЗОР ИЗ ВИДЖЕТОВ (п. 4) =================
const WG_DEF=[['status','Состояние VPN'],['isp','Подключения'],['hero','Текущий сервер и роутер'],['watch','Сторож'],['traffic','Трафик и события']];
function wgCfg(){let c;try{c=JSON.parse(store.get('homeWg',''))}catch(e){}c=c||{};c.order=(c.order||[]).filter(id=>WG_DEF.some(w=>w[0]===id));
  WG_DEF.forEach(w=>{if(!c.order.includes(w[0]))c.order.push(w[0])});c.hidden=c.hidden||[];c.collapsed=c.collapsed||[];return c}
function wgSave(c){store.set('homeWg',JSON.stringify(c))}
function applyWidgets(el){const c=wgCfg();
  c.order.forEach(id=>{const w=el.querySelector(`:scope>[data-wg="${id}"]`);if(!w)return;el.appendChild(w);
    const title=(WG_DEF.find(x=>x[0]===id)||[])[1];const hid=c.hidden.includes(id),col=c.collapsed.includes(id);
    if(!w.innerHTML.trim()){w.style.display='none';return}
    const wrap=document.createElement('div');wrap.className='wg'+(col?' col':'');wrap.dataset.wgw=id;if(hid)wrap.style.display='none';
    w.replaceWith(wrap);wrap.innerHTML=`<div class="wgh"><span class="wgt" data-wgcol="${id}">${col?'▸':'▾'} ${esc(title)}${col?' <span class="note">— свёрнуто</span>':''}</span><span class="sp"></span><button data-wgcol="${id}">${col?'▸ Развернуть':'▾ Свернуть'}</button><button data-wghide="${id}">✕ Скрыть</button></div>`;
    if(!col)wrap.appendChild(w)});
  const b=document.createElement('div');b.className='tools';b.style.margin='4px 0 20px';
  b.innerHTML=`<button class="btn sm" id="wgSetup">⚙ Настроить обзор</button>${c.hidden.length?`<span class="note">скрыто: ${c.hidden.length}</span>`:''}`;el.appendChild(b)}
function openWgSetup(){const c=wgCfg();const box=document.createElement('div');box.className='devmodal';
  const draw=()=>{box.innerHTML=`<div class="card devcard" style="width:min(460px,calc(100vw - 24px))"><h3>Что показывать на обзоре</h3>
   <div class="note" style="margin-bottom:8px">Перетаскивайте строки (или стрелками), чтобы поменять порядок. Запоминается на этом устройстве.</div>
   <div id="wgList">${c.order.map((id,i)=>`<div class="wgrow" draggable="true" data-wid="${id}"><span class="drag">⠿</span><label class="svc-i" style="flex:1"><input type="checkbox" data-wvis="${id}" ${c.hidden.includes(id)?'':'checked'}> ${esc((WG_DEF.find(x=>x[0]===id)||[])[1])}</label><button class="btn sm" data-wmv="${i}" data-d="-1" ${i?'':'disabled'}>↑</button><button class="btn sm" data-wmv="${i}" data-d="1" ${i<c.order.length-1?'':'disabled'}>↓</button></div>`).join('')}</div>
   <div class="tools" style="margin-top:12px;justify-content:space-between"><button class="btn" data-x="reset">Как было</button><button class="btn pri" data-x="ok">Готово</button></div></div>`};
  draw();document.body.appendChild(box);let dragId=null;
  box.addEventListener('dragstart',e=>{const r=e.target.closest('[data-wid]');if(r){dragId=r.dataset.wid;e.dataTransfer.effectAllowed='move'}});
  box.addEventListener('dragover',e=>{if(dragId)e.preventDefault()});
  box.addEventListener('drop',e=>{const r=e.target.closest('[data-wid]');if(!r||!dragId)return;e.preventDefault();const from=c.order.indexOf(dragId),to=c.order.indexOf(r.dataset.wid);c.order.splice(from,1);c.order.splice(to,0,dragId);dragId=null;wgSave(c);draw();renderPage()});
  box.addEventListener('change',e=>{const v=e.target.closest('[data-wvis]');if(!v)return;const id=v.dataset.wvis;c.hidden=v.checked?c.hidden.filter(x=>x!==id):[...c.hidden,id];wgSave(c);renderPage()});
  box.addEventListener('click',e=>{const m=e.target.closest('[data-wmv]'),x=e.target.closest('[data-x]');
    if(m){const i=+m.dataset.wmv,j=i+(+m.dataset.d);[c.order[i],c.order[j]]=[c.order[j],c.order[i]];wgSave(c);draw();renderPage();return}
    if(e.target===box||(x&&x.dataset.x==='ok')){box.remove();return}
    if(x&&x.dataset.x==='reset'){store.set('homeWg','');box.remove();renderPage()}});
}
document.addEventListener('click',e=>{if(page!=='home')return;const t=e.target.closest('[data-wgcol],[data-wghide],#wgSetup');if(!t)return;const c=wgCfg();
  if(t.id==='wgSetup'){openWgSetup();return}
  if(t.dataset.wgcol){const id=t.dataset.wgcol;c.collapsed=c.collapsed.includes(id)?c.collapsed.filter(x=>x!==id):[...c.collapsed,id];wgSave(c);renderPage();return}
  if(t.dataset.wghide){c.hidden=[...new Set([...c.hidden,t.dataset.wghide])];wgSave(c);toast('Скрыто. Вернуть — «⚙ Настроить обзор» внизу');renderPage()}});

// ================= УЧЕБНЫЙ РЕЖИМ (п. 14) =================
function demoBanner(){const el=$('#demoBar');if(!el)return;if(!ABOUT||!ABOUT.demo){el.innerHTML='';return}
  if(el.dataset.on)return;el.dataset.on='1';
  el.innerHTML=`<div class="demobar">🎓 Учебный режим — это копия: изменения не попадают в роутер, XKeen не перезапускается, Telegram молчит.<span style="flex:1"></span><button class="btn sm" id="demoDiff">Показать разницу</button><button class="btn sm" id="demoReset">Сбросить учебную копию</button></div>`}
document.addEventListener('click',async e=>{const t=e.target.closest('#demoDiff,#demoReset');if(!t)return;
  if(t.id==='demoReset'){if(!confirm('Вернуть учебную копию к настоящим настройкам роутера? Все учебные изменения пропадут.'))return;try{await api('/api/demo',{});toast('Учебная копия сброшена');loadState();loadRules&&loadRules()}catch(err){toast(err.message)}return}
  let d;try{d=await api('/api/demo')}catch(err){toast(err.message);return}
  const box=document.createElement('div');box.className='devmodal';
  box.innerHTML=`<div class="card devcard" style="width:min(900px,calc(100vw - 24px));max-height:calc(100vh - 40px);overflow:auto"><h3>Чем учебная копия отличается от роутера<span class="r"><button class="btn icon" data-x="1">✕</button></span></h3>
   ${!(d.files||[]).length&&!d.rulesChanged?'<div class="note">Отличий нет.</div>':''}
   ${(d.files||[]).map(f=>`<div style="margin:10px 0"><b>${esc(f.name)}</b> <span class="tag">${{changed:'изменён',added:'новый',removed:'удалён'}[f.state]}</span>
     ${(f.removed||[]).length||(f.added||[]).length?`<pre class="log" style="max-height:220px">${[...(f.removed||[]).map(x=>'− '+esc(x)),...(f.added||[]).map(x=>'+ '+esc(x))].join('\n')}</pre>`:''}</div>`).join('')}
   ${d.rulesChanged?'<div class="note">Изменены также сервисы и режимы в «Правилах».</div>':''}
   <div class="note" style="margin-top:10px">Учебная копия лежит в <code>${esc(d.dir)}</code>. Удалить её одной командой (по SSH): <code>${esc(d.remove)}</code></div></div>`;
  document.body.appendChild(box);box.onclick=ev=>{if(ev.target===box||ev.target.closest('[data-x]'))box.remove()}});

// ================= ОБНОВЛЕНИЕ ПАНЕЛИ: окно с ходом процесса =================
// Шаги: загрузка (с процентами) → проверка архива на роутере → перезапуск → ждём, пока панель
// снова ответит с новой версией → перезагрузка страницы.
function runPanelUpdate(o){
  const steps=[o.github?'Скачиваю с GitHub':'Загружаю файл на роутер','Проверяю архив на роутере','Перезапускаю панель','Жду, пока панель снова ответит'];
  const box=document.createElement('div');box.className='devmodal';box.style.zIndex=70;document.body.appendChild(box);
  let cur=0,pct=null,msg='',err='',done=false,newVer='';const t0=Date.now();
  const draw=()=>{const sec=Math.round((Date.now()-t0)/1000);
    box.innerHTML=`<div class="card devcard" style="width:min(480px,calc(100vw - 24px))"><h3>Обновление панели<span class="r note mono">${sec} с</span></h3>
     ${steps.map((s,k)=>`<div class="row" style="padding:7px 0"><span>${k<cur||done?'<span class="g">✓</span>':k===cur&&!err?'<span class="spin"></span>':k===cur&&err?'<span class="b">✕</span>':'<span class="n">·</span>'} ${s}</span><span class="mono note">${k===0&&cur===0&&pct!=null?pct+'%':''}</span></div>`).join('')}
     ${cur===0&&pct!=null?`<div class="prog" style="margin:6px 0"><i style="width:${pct}%"></i></div>`:''}
     ${msg&&!err&&!done?`<div class="note" style="margin-top:8px">${msg}</div>`:''}
     ${err?`<div class="banner" style="margin-top:10px">${esc(err)}</div>`:''}
     ${done?`<div class="note g" style="margin-top:10px">✓ Готово: установлена версия ${esc(newVer)}. Страница обновится сама.</div>`:''}
     ${err?'<div class="tools" style="margin-top:12px;justify-content:flex-end"><button class="btn" data-x="1">Закрыть</button></div>':''}</div>`};
  const tick=setInterval(draw,1000);draw();
  box.onclick=e=>{if(e.target.closest('[data-x]')){clearInterval(tick);box.remove()}};
  const fail=e=>{err=e;clearInterval(tick);draw()};
  const waitBack=async want=>{cur=2;msg='Связь с панелью пропадёт на несколько секунд — это нормально.';draw();
    await new Promise(r=>setTimeout(r,3000));cur=3;draw();
    for(let i=0;i<60;i++){try{const r=await fetch('/api/about',{cache:'no-store'});if(r.ok){const j=await r.json();if(!want||j.version===want){newVer=j.version;done=true;clearInterval(tick);draw();setTimeout(()=>location.reload(),2500);return}}}catch(e){}
      await new Promise(r=>setTimeout(r,2000))}
    fail('Панель не ответила за 2 минуты. Обновите страницу вручную; если не открывается — по SSH: /opt/etc/init.d/S99xray-selector restart')};
  if(o.github){msg='Скачиваю архив — через VPN, если не выйдет, напрямую.';draw();
    api('/api/update',{action:'install'}).then(r=>{cur=2;waitBack(r.version)}).catch(e=>fail(e.message+' — панель осталась на прежней версии.'));return}
  const xhr=new XMLHttpRequest();const fd=new FormData();fd.append('file',o.file);
  xhr.upload.onprogress=e=>{if(e.lengthComputable){pct=Math.round(e.loaded*100/e.total);if(pct>=100&&cur===0){cur=1;msg='Архив получен. Роутер берёт файл для своей архитектуры и проверяет, что он запускается (10–30 с).'}draw()}};
  xhr.onload=()=>{let j={};try{j=JSON.parse(xhr.responseText)}catch(e){}
    if(xhr.status!==200){cur=Math.max(cur,1);fail((j.error||('ошибка '+xhr.status))+' — панель осталась на прежней версии.');return}waitBack(j.version)};
  xhr.onerror=()=>fail('Связь с роутером прервалась во время загрузки. Попробуйте ещё раз.');
  xhr.open('POST','/api/self-update');xhr.send(fd);
}

// ================= Журнал: подсказки устройств в фильтре (0.9) =================
function lgSugShow(){const box=$('#lgSug'),inp=$('#lgQ');if(!box||!inp)return;
  if(!DEVS){loadDevices().then(()=>{if(document.activeElement===inp)lgSugShow()});return}
  const q=inp.value.trim().toLowerCase();const now=Date.now()/1000;
  let rec=[];try{rec=JSON.parse(store.get('lgRecent','[]'))}catch(e){}
  const l=DEVS.filter(d=>!d.mergeInto).map(d=>({n:d.name||d.kname||'',ip:d.ip||'',on:now-(d.lastSeen||0)<300})).filter(d=>d.n||d.ip)
    .filter(d=>!q||d.n.toLowerCase().includes(q)||d.ip.includes(q))
    .sort((a,b)=>(b.on-a.on)||((rec.indexOf(b.n)>=0)-(rec.indexOf(a.n)>=0))||a.n.localeCompare(b.n)).slice(0,12);
  const chips=`<div class="sugc"><button data-lgm="vpn">только VPN</button><button data-lgm="direct">только напрямую</button><button data-lgm="block">заблокированные</button>${rec.slice(0,3).map(n=>`<button data-lgv="${esc(n)}">${esc(n)}</button>`).join('')}</div>`;
  box.innerHTML=chips+(l.length?l.map(d=>`<div class="sugi" data-lgv="${esc(d.n||d.ip)}">${d.on?'<span class="dot live" style="display:inline-block;margin-right:6px"></span>':''}<b>${esc(d.n||'Без имени')}</b> <span class="note mono">${esc(d.ip)}</span></div>`).join(''):'<div class="note" style="padding:6px 10px">нет таких устройств — фильтр сработает по тексту</div>');
  box.style.display=''}
document.addEventListener('focusin',e=>{if(e.target.id==='lgQ')lgSugShow()});
document.addEventListener('input',e=>{if(e.target.id==='lgQ')lgSugShow()});
document.addEventListener('mousedown',e=>{const box=$('#lgSug');if(!box||box.style.display==='none')return;
  const v=e.target.closest('[data-lgv]'),m=e.target.closest('[data-lgm]');
  if(v||m){e.preventDefault();
    if(v){$('#lgQ').value=v.dataset.lgv;let rec=[];try{rec=JSON.parse(store.get('lgRecent','[]'))}catch(_){}rec=[v.dataset.lgv,...rec.filter(x=>x!==v.dataset.lgv)].slice(0,5);store.set('lgRecent',JSON.stringify(rec))}
    if(m&&$('#lgMode')){$('#lgMode').value=m.dataset.lgm}
    box.style.display='none';$('#lgQ').blur();loadLogs();return}
  if(!e.target.closest('.lgqwrap'))box.style.display='none'});
document.addEventListener('keydown',e=>{if(e.target.id==='lgQ'&&(e.key==='Escape'||e.key==='Enter')){const b=$('#lgSug');if(b)b.style.display='none'}});

// ================= ЗАПУСК =================
applyTheme(); renderNav(); go(location.hash.length>1?decodeURIComponent(location.hash.slice(1)):pageKey); window.addEventListener('hashchange',()=>{if(location.hash.length>1)go(decodeURIComponent(location.hash.slice(1)))}); loadAbout(); loadUpd(); setInterval(loadAbout,60000); setInterval(loadUpd,600000);
matchMedia('(max-width:900px)').addEventListener('change',()=>{applyTheme();renderPage()});
maybeWizard();
(async function poll(){await loadState();setTimeout(poll,S&&(S.testing.length||(W&&W.running))?1500:5000)})();
(function pm(){loadMetrics();setTimeout(pm,page==='home'?2000:10000)})();
loadTraffic();setInterval(loadTraffic,30000);
loadFlow();setInterval(()=>{if(page==='map')loadFlow()},10000);
setInterval(()=>{if(page==='devices')loadDevices()},15000);
document.addEventListener('visibilitychange',()=>{if(!document.hidden&&page==='map')drawFlowAll()});
</script>
</body>
</html>
