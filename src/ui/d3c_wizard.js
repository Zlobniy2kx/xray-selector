<script>
"use strict";
// ================= МАСТЕР ПЕРВОГО ЗАПУСКА (п. 8) =================
const WZ_STEPS=['Роутер','Пароль','Серверы','Сервисы','Подбор','Резерв','Telegram','Готово'];
let WZ=null;
function wzNew(){return {step:0,info:null,checks:null,pw:'',pw2:'',pwLater:false,source:'',input:'',job:null,vpn:new Set(),novpn:new Set(),pick:null,picked:[],
  tun:null,tunSel:new Set(),tunRes:{},tgTok:'',tgLevel:'important',tgMsg:'',log:[],applying:false,finished:false}}
async function openWizard(){WZ=wzNew();let box=$('#wizard');if(!box){box=document.createElement('div');box.id='wizard';box.className='devmodal wizard';document.body.appendChild(box)}
  try{WZ.info=await api('/api/wizard')}catch(e){WZ.info={}}
  if(!RUL)await loadRules();
  // первый запуск — ничего не отмечено (п. 8.4); повторный — текущие правила, чтобы ничего не потерять
  if(RUL&&RUL.state){(RUL.state.vpn||[]).forEach(id=>WZ.vpn.add(id));(RUL.state.novpn||[]).forEach(id=>WZ.novpn.add(id))}
  wzGo(0)}
function wzClose(){const b=$('#wizard');if(b)b.remove();clearTimeout(WZ&&WZ._t);WZ=null;renderPage()}
async function wzGo(i){WZ.step=i;clearTimeout(WZ._t);wzDraw();
  if(i===0&&!WZ.checks){try{WZ.checks=(await api('/api/wizard?checks=1')).checks}catch(e){WZ.checks=[{name:'Панель',ok:false,info:e.message}]}wzDraw()}
  if(i===5&&!WZ.tun){try{const r=await api('/api/reserve');WZ.tun=r.ifaces||[];(r.configured||[]).forEach(c=>WZ.tunSel.add(c.iface))}catch(e){WZ.tun=[]}wzDraw();
    for(const t of WZ.tun){if(!WZ||WZ.step!==5)break;WZ.tunRes[t.name]='…';wzDraw();try{const r=await api('/api/reserve',{check:t.name});WZ.tunRes[t.name]=r.ok?'ok':(r.why||'не открывает сервисы')}catch(e){WZ.tunRes[t.name]=e.message}if(WZ&&WZ.step===5)wzDraw()}}
}
function wzPoll(){clearTimeout(WZ._t);WZ._t=setTimeout(async()=>{if(!WZ)return;try{const j=await api('/api/wizard?job=1');if(WZ.step===2)WZ.job=j;if(WZ.step===4)WZ.pick=j;wzDraw();if(j&&j.running)wzPoll()}catch(e){}},1200)}
const SVCS=['YT','WA','TG','IG','GPT','Claude'];
function wzTable(j){if(!j)return '';const rows=(j.rows||[]).slice().sort((a,b)=>(b.okn-a.okn)||((a.alive?a.ms:9e9)-(b.alive?b.ms:9e9)));
  return `<div style="max-height:42vh;overflow:auto;margin-top:10px"><table class="t"><thead><tr><th>Сервер</th><th>Живой</th><th>Пинг</th>${SVCS.map(s=>`<th>${s}</th>`).join('')}</tr></thead><tbody>
   ${rows.map(r=>{const checked=r.alive||r.err||!j.running;return `<tr><td>${srvName(r.tag,true)}</td><td>${!checked?'<span class="spin"></span>':r.alive?'<span class="g">✓</span>':r.err?'<span class="b" title="'+esc(r.err)+'">✕</span>':'<span class="n">—</span>'}</td><td class="mono">${r.alive?`<span class="${clsMs(r.ms)}">${r.ms}</span>`:'—'}</td>${SVCS.map(s=>`<td>${r.svc&&s in r.svc?(r.svc[s]?'<span class="g">✓</span>':'<span class="b">✕</span>'):'<span class="n">·</span>'}</td>`).join('')}</tr>`}).join('')}</tbody></table></div>`}
function wzWorking(j){return j?(j.rows||[]).filter(r=>r.alive&&r.okn>0).length:0}
function wzDraw(){const box=$('#wizard');if(!box||!WZ)return;const i=WZ.step,inf=WZ.info||{};let body='',next=true,nextTxt='Дальше';
  if(i===0){const c=WZ.checks;body=`<p class="note">Проверяю, всё ли готово на роутере.</p>${!c?'<div class="empty"><span class="spin"></span></div>':`<table class="t">${c.map(x=>`<tr><td>${x.ok?'<span class="g">✓</span>':'<span class="b">✕</span>'}</td><td><b>${esc(x.name)}</b><div class="note">${esc(x.info||'')}</div></td></tr>`).join('')}</table>
    ${c.some(x=>!x.ok)?'<div class="banner" style="margin-top:10px">Не всё в порядке, но мастер можно пройти дальше. То, что отмечено ✕, понадобится для полной работы панели.</div>':'<div class="note g" style="margin-top:10px">Всё готово.</div>'}`}`}
  if(i===1){if(inf.pw){body='<div class="note">Пароль на панель уже задан. Сменить или снять его можно в «Настройки → Пароль».</div>'}
    else body=`<p class="note">Пароль защищает панель от других устройств в домашней сети. Он необязателен.</p>
    <div class="fg"><label>Пароль<input class="in" type="password" id="wzPw" value="${esc(WZ.pw)}" ${WZ.pwLater?'disabled':''} autocomplete="new-password"></label><label>Ещё раз<input class="in" type="password" id="wzPw2" value="${esc(WZ.pw2)}" ${WZ.pwLater?'disabled':''} autocomplete="new-password"></label></div>
    <label class="svc-i" style="margin-top:10px"><input type="checkbox" id="wzPwLater" ${WZ.pwLater?'checked':''}> Установлю пароль позже в настройках</label>
    <div class="note b" id="wzPwErr" style="margin-top:6px"></div>`}
  if(i===2){const j=WZ.job,run=j&&j.running;const X=wzWorking(j),Y=j?j.total:0;
    body=`<p class="note">Вставьте ссылку на подписку от вашего VPN-сервиса или ссылки на серверы (по одной в строке).</p>
    <textarea class="in" id="wzIn" style="min-height:84px" placeholder="https://sub.example.com/abc123   — подписка
vless://uuid@host:443?type=tcp&security=reality&…#🇩🇪 Германия
trojan://pass@host:443#🇳🇱 Нидерланды
ss://…  или  vmess://…">${esc(WZ.input)}</textarea>
    <div class="tools" style="margin-top:8px"><button class="btn pri" id="wzCheck" ${run?'disabled':''}>Проверить</button><span class="note">или</span><button class="btn" id="wzXkeen" ${run?'disabled':''}>У меня уже есть серверы в XKeen</button></div>
    <div class="note" style="margin-top:4px">Ничего вводить не нужно — панель возьмёт серверы, которые уже работают в вашем XKeen, и сразу их проверит.</div>
    ${j?`<div style="margin-top:12px">${run?`<span class="spin"></span> проверено ${j.done} из ${j.total}`:j.err?`<span class="b">${esc(j.err)}</span>`:`<b class="${X?'g':'b'}" style="font-size:16px">Работают ${X} из ${Y}</b>`}<div class="prog" style="margin-top:6px"><i style="width:${Y?Math.round(j.done*100/Y):0}%"></i></div></div>${wzTable(j)}`:''}
    ${j&&!run?`<div class="banner" style="margin-top:10px">Если серверы не работают или работают не все — напишите в поддержку вашего VPN-сервиса. Ссылку на подписку можно поменять потом: «Настройки → Подписка».</div>`:''}`;
    next=!run;}
  if(i===3){const svcs=(RUL&&RUL.services)||[];body=`<p class="note">Что пустить через VPN, а что — всегда через обычный интернет. Всё остальное будет открываться как обычно, без VPN. Поменять можно потом в разделе «Правила».</p>
    <div class="grid g2"><div><h3 style="margin:6px 0">Через VPN</h3><div id="wzVPN">${catalogHTML(svcs,['popular','social','ai','media','work','games','other'],WZ.vpn)}</div></div>
    <div><h3 style="margin:6px 0">Без VPN — через вашего провайдера</h3><div class="note" style="margin-bottom:6px">Эти сайты всегда открываются через ваш обычный интернет, как будто VPN нет: банки и Госуслуги не блокируют вход «из-за границы», игровые серверы дают меньший пинг.</div><div id="wzNO">${catalogHTML(svcs,['novpn'],WZ.novpn)}</div></div></div>`}
  if(i===4){const p=WZ.pick,run=p&&p.running;body=`<p class="note">Панель проверит все серверы на сервисах и скорости и выберет 6 разных лучших — по возможности из разных стран. Это займёт 1–2 минуты.</p>
    ${!p?'<button class="btn pri" id="wzPick">Подобрать серверы</button>':''}
    ${p?`<div>${run?`<span class="spin"></span> проверено ${p.done} из ${p.total}${p.done>=p.total?' · меряю скорость лучших…':''}`:p.err?`<span class="b">${esc(p.err)}</span>`:'<b class="g">Готово</b>'}<div class="prog" style="margin-top:6px"><i style="width:${p.total?Math.round(p.done*100/p.total):0}%"></i></div></div>`:''}
    ${p&&!run&&(p.picked||[]).length?`<div style="margin-top:12px"><b>Сторож будет держать эти серверы:</b>${(p.picked||[]).map(t=>{const r=(p.rows||[]).find(x=>x.tag===t)||{};return `<div class="row"><span>${srvName(t,true)}</span><span class="note mono">${r.ms||'—'} мс · сервисы ${r.okn}/6${r.kbps?' · '+mbps(r.kbps)+' Мбит/с':''}</span></div>`}).join('')}</div>`:''}
    ${p&&!run&&!(p.picked||[]).length?'<div class="banner" style="margin-top:10px">Не нашёл ни одного рабочего сервера. Можно идти дальше и настроить сторож позже.</div>':''}`;next=!run}
  if(i===5){const l=WZ.tun;body=`<p class="note">Если все серверы подписки перестанут работать, сторож переключится на подключение Keenetic (WireGuard, OpenVPN, прокси…). Отметьте, какие можно использовать.</p>
    ${!l?'<div class="empty"><span class="spin"></span></div>':l.length?`<table class="t">${l.map(t=>{const r=WZ.tunRes[t.name];return `<tr><td><input type="checkbox" class="wzTun" value="${esc(t.name)}" ${WZ.tunSel.has(t.name)?'checked':''}></td><td><b>${esc(t.type)} · ${esc(t.id||t.name)}</b>${t.desc?' <span class="note">'+esc(t.desc)+'</span>':''}<div class="note">${t.up?'подключено':'не подключено'}</div></td><td>${r==='…'?'<span class="spin"></span>':r==='ok'?'<span class="g">✓ работает</span>':r?'<span class="b">✕ '+esc(r)+'</span>':''}</td></tr>`}).join('')}</table>`:'<div class="note">Подключений WireGuard / OpenVPN / прокси в Keenetic не найдено — шаг можно пропустить.</div>'}`}
  if(i===6){body=`<p class="note">Telegram-бот пришлёт сообщение, если VPN перестанет работать. Необязательно.</p>
    <ol class="note" style="line-height:1.7;padding-left:18px"><li>В Telegram откройте <b>@BotFather</b> → /newbot → получите токен.</li><li>Напишите своему боту /start (или добавьте его в семейную группу и напишите там /start).</li><li>Вставьте токен и нажмите «Подключить».</li></ol>
    <div class="tools"><input class="in" id="wzTok" placeholder="123456789:AA…" value="${esc(WZ.tgTok)}" style="flex:1;min-width:200px"><button class="btn pri" id="wzTg">Подключить</button><button class="btn" id="wzTgTest">Отправить тест</button></div>
    <div class="note" id="wzTgMsg" style="margin-top:6px">${WZ.tgMsg||''}</div>
    <label style="display:block;margin-top:10px" class="note">Что присылать<select class="in" id="wzLev" style="display:block;margin-top:4px"><option value="crit">Только аварии</option><option value="important">Важное (по умолчанию)</option><option value="all">Всё</option></select></label>`}
  if(i===7){const items=[];
    if(!inf.pw)items.push(WZ.pwLater||!WZ.pw?'Пароль — позже, в настройках':'Задать пароль на панель');
    if(WZ.source==='input')items.push('Записать серверы из '+((WZ.input.trim().startsWith('http')&&!WZ.input.trim().includes('\n'))?'подписки (и обновлять её автоматически)':'ваших ссылок'));
    items.push(`Правила: через VPN — ${WZ.vpn.size} сервисов, без VPN — ${WZ.novpn.size}`);
    if(WZ.picked.length)items.push('Сторож: '+WZ.picked.length+' серверов, включён');
    if(WZ.tunSel.size)items.push('Резерв: '+WZ.tunSel.size+' подключений Keenetic');
    items.push('Telegram: '+({crit:'только аварии',important:'важное',all:'всё'}[WZ.tgLevel]));
    body=WZ.finished?`<div style="text-align:center;padding:20px 0"><div style="font-size:42px">✅</div><h3>Готово!</h3><p class="note">Панель настроена. Всё можно поменять в любой момент.</p></div><pre class="log" style="max-height:30vh">${esc(WZ.log.join('\n'))}</pre>`
      :`<p class="note">Проверьте и нажмите «Применить». Каждое изменение конфига проверяется Xray, перед записью делается резервная копия; Xray перезапустится 2–3 раза (связь пропадёт на 5–10 секунд).</p><ul>${items.map(x=>`<li style="margin:4px 0">${esc(x)}</li>`).join('')}</ul>${WZ.log.length?`<pre class="log" style="max-height:30vh">${esc(WZ.log.join('\n'))}</pre>`:''}`;
    nextTxt=WZ.finished?'Закрыть':WZ.applying?'Применяю…':'Применить';next=!WZ.applying}
  box.innerHTML=`<div class="card wzcard"><div class="wzsteps">${WZ_STEPS.map((s,k)=>`<span class="${k===i?'on':k<i?'done':''}">${k+1}. ${s}</span>`).join('')}</div>
   <h3 style="font-size:18px;margin:12px 0 8px">${WZ_STEPS[i]}</h3><div class="wzbody">${body}</div>
   <div class="tools wzfoot"><button class="btn" data-wz="close">${WZ.finished?'':'Позже'}</button><span class="sp"></span>${i>0&&i<7&&!WZ.applying?'<button class="btn" data-wz="skip" title="Ничего не менять на этом шаге">Пропустить шаг</button>':''}${i>0&&!WZ.applying&&!WZ.finished?'<button class="btn" data-wz="back">Назад</button>':''}<button class="btn pri" data-wz="next" ${next?'':'disabled'}>${nextTxt}</button></div></div>`;
  fixIndeterminate(box);if(i===6)$('#wzLev').value=WZ.tgLevel;
}
async function wzApply(){WZ.applying=true;WZ.log=[];const L=t=>{WZ.log.push(t);wzDraw()};wzDraw();const inf=WZ.info||{};
  const step=async(name,fn)=>{L('… '+name);try{const r=await fn();WZ.log[WZ.log.length-1]='✓ '+name+(r?' — '+r:'');wzDraw();return true}catch(e){WZ.log[WZ.log.length-1]='✕ '+name+': '+e.message;wzDraw();return false}};
  if(!inf.pw&&!WZ.pwLater&&WZ.pw)await step('Пароль',async()=>{await api('/api/password',{old:'',new:WZ.pw});PW=null});
  if(WZ.source==='input')await step('Серверы',async()=>{const r=await api('/api/wizard',{action:'apply-servers',input:WZ.input});return `+${r.added}, −${r.removed}`});
  if(WZ.vpn.size||WZ.novpn.size)await step('Правила',async()=>{const r=await api('/api/rules',{action:'save',mode:1,vpn:[...WZ.vpn],novpn:[...WZ.novpn]});return r.nochange?'без изменений':''});
  await loadState();const g=mainGroup();
  if(WZ.picked.length&&g)await step('Сторож',async()=>{const c=(S.servers||[]).map(s=>s.tag);const l=WZ.picked.filter(t=>c.includes(t));if(!l.length)throw new Error('выбранных серверов нет в конфиге');
    await api('/api/watch',{group:g.tag,servers:l});await api('/api/watch',{group:g.tag,enabled:true});return l.length+' серверов'});
  if(WZ.tunSel.size)await step('Резерв',async()=>{await api('/api/reserve',{ifaces:[...WZ.tunSel],auto:false})});
  await step('Telegram',async()=>{const st=(await api('/api/watch')).settings||W.settings||{};await api('/api/watch',{settings:{...st,tgLevel:WZ.tgLevel,tgEvents:'all',wizardDone:true}})});
  await step('Мастер пройден',async()=>{await api('/api/wizard',{action:'done'})});
  WZ.applying=false;WZ.finished=true;wzDraw();loadState();loadRules()}
document.addEventListener('input',e=>{if(!WZ)return;const id=e.target.id;
  if(id==='wzPw')WZ.pw=e.target.value;if(id==='wzPw2')WZ.pw2=e.target.value;if(id==='wzIn')WZ.input=e.target.value;if(id==='wzTok')WZ.tgTok=e.target.value});
document.addEventListener('change',e=>{if(!WZ)return;const t=e.target;
  if(t.id==='wzPwLater'){WZ.pwLater=t.checked;if(t.checked){WZ.pw='';WZ.pw2=''}wzDraw();return}
  if(t.id==='wzLev'){WZ.tgLevel=t.value;return}
  if(t.classList.contains('wzTun')){t.checked?WZ.tunSel.add(t.value):WZ.tunSel.delete(t.value);return}
  if(t.closest('#wzVPN')){catalogClick(e,WZ.vpn,()=>{WZ.vpn.forEach(id=>WZ.novpn.delete(id));wzDraw()});return}
  if(t.closest('#wzNO')){catalogClick(e,WZ.novpn,()=>{WZ.novpn.forEach(id=>WZ.vpn.delete(id));wzDraw()});return}});
document.addEventListener('click',async e=>{if(!WZ)return;const b=e.target.closest('[data-wz],#wzCheck,#wzXkeen,#wzPick,#wzTg,#wzTgTest');if(!b)return;
  if(b.id==='wzCheck'||b.id==='wzXkeen'){WZ.source=b.id==='wzXkeen'?'xkeen':'input';WZ.pick=null;WZ.picked=[];
    try{await api('/api/wizard',{action:'check',source:WZ.source,input:WZ.input});WZ.job={running:true,done:0,total:0,rows:[]};wzDraw();wzPoll()}catch(err){WZ.job={err:err.message,rows:[]};wzDraw()}return}
  if(b.id==='wzPick'){try{await api('/api/wizard',{action:'pick',source:WZ.source||'xkeen'});WZ.pick={running:true,done:0,total:0};wzDraw();wzPoll()}catch(err){WZ.pick={err:err.message};wzDraw()}return}
  if(b.id==='wzTg'){WZ.tgMsg='<span class="spin"></span>';wzDraw();try{const r=await api('/api/telegram',{token:WZ.tgTok,action:'test'});WZ.tgMsg='<span class="g">✓ Подключено'+(r.chat?': '+esc(r.chat):'')+'</span>'}catch(err){WZ.tgMsg='<span class="b">'+esc(err.message)+'</span>'}wzDraw();return}
  if(b.id==='wzTgTest'){try{const r=await api('/api/telegram',{action:'sendtest'});WZ.tgMsg=r.ok?'<span class="g">✓ Тест отправлен — проверьте Telegram</span>':'<span class="b">'+esc(r.adminErr||r.groupErr||'не доставлено')+'</span>'}catch(err){WZ.tgMsg='<span class="b">'+esc(err.message)+'</span>'}wzDraw();return}
  const a=b.dataset.wz;
  if(a==='close'){if(WZ.applying)return;wzClose();return}
  if(a==='back'){wzGo(WZ.step-1);return}
  if(a==='skip'){const i=WZ.step;if(i===1){WZ.pwLater=true;WZ.pw=WZ.pw2=''}if(i===2){WZ.source=''}if(i===3){WZ.vpn.clear();WZ.novpn.clear()}if(i===4){WZ.picked=[];WZ.pick=null}if(i===5)WZ.tunSel.clear();wzGo(i+1);return}
  if(a==='next'){const i=WZ.step;
    if(i===1&&!(WZ.info||{}).pw&&!WZ.pwLater){if(!WZ.pw){$('#wzPwErr').textContent='Введите пароль или отметьте «Установлю пароль позже»';return}
      if(WZ.pw.length<6){$('#wzPwErr').textContent='Пароль — хотя бы 6 символов';return}if(WZ.pw!==WZ.pw2){$('#wzPwErr').textContent='Пароли не совпадают';return}}
    if(i===4&&WZ.pick&&!WZ.pick.running)WZ.picked=WZ.pick.picked||[];
    if(i===7){if(WZ.finished){wzClose();return}await wzApply();return}
    wzGo(i+1)}});
// автозапуск на чистой установке
async function maybeWizard(){try{const r=await api('/api/wizard');if(!r.done&&!r.watch&&!$('#wizard'))openWizard()}catch(e){}}
</script>
