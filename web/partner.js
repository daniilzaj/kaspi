const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];
const state = {tenantId:null,testedConfig:null};
const esc = (value) => String(value ?? '').replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
const date = (value) => value ? new Date(value).toLocaleDateString('ru-RU',{day:'numeric',month:'short',year:'numeric'}) : '—';
const eventWord = (count) => count % 100 >= 11 && count % 100 <= 14 ? 'событий' : count % 10 === 1 ? 'событие' : count % 10 >= 2 && count % 10 <= 4 ? 'события' : 'событий';
async function api(path, options={}) {
  const response = await fetch(path,options);
  let data;
  try { data = await response.json(); } catch { throw new Error('Сервер вернул некорректный ответ'); }
  if (!response.ok) throw new Error(data.error || 'Не удалось выполнить запрос');
  return data;
}
function toast(message,error=false) {
  const target=$('#toast');target.textContent=message;target.className=`show${error?' error':''}`;
  clearTimeout(window.toastTimer);window.toastTimer=setTimeout(()=>target.className='',4500);
}
function result(message,error=false) {
  const target=$('#upload-result');target.className=`upload-result${error?' error':''}`;target.textContent=message;
}
async function selectTenant() {
  state.tenantId=Number($('#partner-tenant').value);
  state.testedConfig=null;$('#connector-save').disabled=true;
  $('#connector-preview').classList.add('hidden');
  $('#upload-tenant').value=state.tenantId;
  await Promise.all([loadImports(),loadConnectors()]);
}
async function loadTenants() {
  const tenants=await api('/api/tenants');
  $('#partner-tenant').innerHTML=tenants.map(tenant=>`<option value="${tenant.id}">${esc(tenant.name)}</option>`).join('');
  if(tenants.length)await selectTenant();
}
async function loadImports() {
  const imports=await api(`/api/imports?tenant_id=${state.tenantId}`);
  $('#imports-table').innerHTML=imports.length?imports.map(item=>`<tr><td data-label="Файл"><strong>${esc(item.filename)}</strong></td><td data-label="Обработано">${item.ok} / ${item.total}</td><td data-label="Ошибки">${item.failed?`<span class="tag muted-tag" title="${esc(item.errors)}">${item.failed}</span>`:'—'}</td><td data-label="Дата">${date(item.date)}</td></tr>`).join(''):'<tr><td colspan="4" class="empty">Загрузок пока нет. Выберите первый отчёт выше.</td></tr>';
}
async function upload(event) {
  event.preventDefault();
  const form=$('#upload-form');
  try {
    const response=await api('/api/import',{method:'POST',body:new FormData(form)});
    result(`Отчёт обработан: ${response.ok} из ${response.total} строк. Ошибок: ${response.failed}.${response.errors?.length?' '+response.errors.join('; '):''}`,response.failed>0);
    form.reset();$('#upload-tenant').value=state.tenantId;$('#chosen-file').textContent='';
    await loadImports();
  } catch(error) { result(error.message,true); }
}
function connectorConfig() {
  const field_map={};
  $$('[data-map]').forEach(input=>field_map[input.dataset.map]=input.value.trim());
  $('#connector-type-map').value.split(',').map(item=>item.trim()).filter(Boolean).forEach(pair=>{const [from,to]=pair.split('=').map(item=>item.trim());field_map['type:'+from]=to||'';});
  return {tenant_id:state.tenantId,name:$('#connector-name').value.trim(),url:$('#connector-url').value.trim(),array_path:$('#connector-array').value.trim(),auth_header:$('#connector-auth-header').value.trim(),auth_value:$('#connector-auth-value').value.trim(),field_map};
}
function preview(html,error=false) { const target=$('#connector-preview');target.className=`connector-preview${error?' error':''}`;target.innerHTML=html; }
async function testConnector() {
  const config=connectorConfig();state.testedConfig=null;$('#connector-save').disabled=true;
  preview('<strong>Проверяем ответ…</strong>');
  try {
    const check=await api('/api/connectors/test',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(config)});
    const rows=check.preview.map(item=>`${item.event_id} · ${item.event_type} · ${item.name||item.phone||item.email}`).join('\n');
    preview(`<strong>${check.valid?'Проверка прошла':'Есть ошибки в данных'} · ${check.total} ${eventWord(check.total)}</strong>${rows?'<code>'+esc(rows)+'</code>':''}${check.issues.length?'<code>'+esc(check.issues.join('\n'))+'</code>':''}`,!check.valid);
    if(check.valid){state.testedConfig=JSON.stringify(config);$('#connector-save').disabled=false;}
  } catch(error) { preview('<strong>Проверка не прошла</strong>'+esc(error.message),true); }
}
async function saveConnector(event) {
  event.preventDefault();const config=connectorConfig();
  if(JSON.stringify(config)!==state.testedConfig){preview('<strong>Настройки изменились</strong>Проверьте ответ ещё раз.',true);return;}
  try {
    await api('/api/connectors',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(config)});
    toast('Подключение сохранено');$('#connector-form').reset();$('#connector-save').disabled=true;state.testedConfig=null;$('#connector-preview').classList.add('hidden');
    await loadConnectors();
  } catch(error) { preview('<strong>Не удалось сохранить</strong>'+esc(error.message),true); }
}
async function loadConnectors() {
  const connectors=await api(`/api/connectors?tenant_id=${state.tenantId}`);
  $('#connectors-list').innerHTML=connectors.length?connectors.map(item=>`<div class="connector-item"><strong>${esc(item.name)}</strong><small>${esc(item.url)}</small><small>${item.last_result?esc(item.last_result):'Данные ещё не загружались'}</small><div class="sync-line"><span>${item.last_sync_at?date(item.last_sync_at):'Ожидает загрузки'}</span><button data-sync="${item.id}">Загрузить данные</button></div></div>`).join(''):'<p class="empty">API пока не подключён. Вы можете продолжать загружать файлы.</p>';
}
async function syncConnector(id) {
  const button=$(`[data-sync="${id}"]`);if(button)button.disabled=true;
  try { const summary=await api(`/api/connectors/${id}/sync`,{method:'POST'});toast(`Загружено: ${summary.ok}, ошибок: ${summary.failed}`,summary.failed>0);await loadConnectors(); }
  catch(error) { toast(error.message,true); }
  finally { if(button)button.disabled=false; }
}
$('#partner-tenant').addEventListener('change',()=>selectTenant().catch(error=>toast(error.message,true)));
$('#upload-file').addEventListener('change',event=>$('#chosen-file').textContent=event.target.files[0]?.name||'');
$('#upload-form').addEventListener('submit',upload);
$('#connector-test').addEventListener('click',testConnector);
$('#connector-form').addEventListener('submit',saveConnector);
$('#connector-form').addEventListener('input',()=>{state.testedConfig=null;$('#connector-save').disabled=true;});
document.addEventListener('click',event=>{const button=event.target.closest('[data-sync]');if(button)syncConnector(button.dataset.sync);});
loadTenants().catch(error=>toast(error.message,true));
