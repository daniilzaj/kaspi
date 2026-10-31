const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];
const state = { tenants: [], segment: null, page: 'overview' };
const number = (value) => new Intl.NumberFormat('ru-RU').format(value ?? 0);
const money = (value) => new Intl.NumberFormat('ru-RU', {style:'currency',currency:'RUB',minimumFractionDigits:0,maximumFractionDigits:2}).format(value ?? 0);
const phone = (value) => /^7\d{10}$/.test(value || '') ? `+7 ${value.slice(1,4)} ${value.slice(4,7)}-${value.slice(7,9)}-${value.slice(9)}` : (value || '');
const date = (value) => value ? new Date(value).toLocaleDateString('ru-RU',{day:'numeric',month:'short',year:'numeric'}) : '—';
const esc = (value) => String(value ?? '').replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
async function api(path, options = {}) {
  const response = await fetch(path, options);
  let data;
  try { data = await response.json(); } catch { throw new Error('Сервер вернул некорректный ответ'); }
  if (!response.ok) throw new Error(data.error || 'Не удалось выполнить запрос');
  return data;
}
function toast(message, error = false) {
  const target = $('#toast');
  target.textContent = message;
  target.className = `show${error ? ' error' : ''}`;
  clearTimeout(window.toastTimer);
  window.toastTimer = setTimeout(() => target.className = '', 4500);
}
function showPage(name) {
  state.page = name;
  $$('.page').forEach(page => page.classList.toggle('active',page.id === `page-${name}`));
  $$('.nav-item').forEach(button => button.classList.toggle('active',button.dataset.page === name));
  window.scrollTo({top:0,behavior:'smooth'});
  refresh();
}
async function refresh() {
  try {
    if (state.page === 'overview') await loadOverview();
    if (state.page === 'customers') await loadCustomers();
    if (state.page === 'audiences') await loadCampaigns();
  } catch (error) { toast(error.message,true); }
}
async function loadTenants() {
  state.tenants = await api('/api/tenants');
  const options = state.tenants.map(tenant => `<option value="${tenant.id}">${esc(tenant.name)}</option>`).join('');
  $('#source-select').innerHTML = options;
  $('#target-select').innerHTML = options;
  if (state.tenants.length > 1) $('#target-select').value = state.tenants[1].id;
}
async function loadOverview() {
  const [metrics,campaigns] = await Promise.all([api('/api/overview'),api('/api/campaigns')]);
  $('#m-profiles').textContent = number(metrics.profiles);
  $('#m-events').textContent = number(metrics.events);
  $('#m-first').textContent = number(metrics.first_purchases);
  $('#m-campaigns').textContent = number(metrics.campaigns);
  $('#m-tenants').textContent = number(metrics.tenants);
  $('#ecosystem').innerHTML = state.tenants.map(tenant => `<div class="flow-tenant"><span class="flow-led"></span><strong>${esc(tenant.name)}</strong><b>${number(tenant.customers)}</b></div>`).join('');
  $('#overview-campaigns').innerHTML = campaigns.length ? campaigns.slice(0,1).map(c => `<div class="campaign-mini"><div><strong>${esc(c.name)}</strong><small>${esc(c.source)} → ${esc(c.target)} · ${c.members} клиентов</small></div><b>${c.converted}</b></div>`).join('') : '<p class="empty">Кампаний пока нет. Начните с поиска аудитории.</p>';
}
async function loadCustomers() {
  const customers = await api('/api/customers?q='+encodeURIComponent($('#customer-search').value));
  $('#customers-count').textContent = `Найдено: ${customers.length}`;
  $('#customers-table').innerHTML = customers.length ? customers.map(c => `<tr><td data-label="Клиент"><strong>${esc(c.name)}</strong><span class="muted">ID ${c.id}</span></td><td data-label="Контакт">${esc(phone(c.phone)||c.email||'—')}</td><td data-label="Проекты">${c.tenant_count}</td><td data-label="События">${c.event_count}</td><td data-label="Сумма покупок">${money(c.spend)}</td><td data-label=""><button class="row-btn" data-customer="${c.id}">Профиль</button></td></tr>`).join('') : '<tr><td colspan="6" class="empty">Клиенты не найдены.</td></tr>';
}
const eventNames = {purchase:'Покупка',visit:'Визит',booking:'Бронирование',topup:'Пополнение',usage:'Использование услуги',refund:'Возврат'};
async function openCustomer(id) {
  try {
    const c = await api(`/api/customers/${id}`);
    $('#detail-title').textContent = c.name;
    $('#detail-body').innerHTML = `<div class="detail-meta"><span>${esc(phone(c.phone)||'Телефон не указан')}</span><span>${esc(c.email||'E-mail не указан')}</span><span>${c.consent?'Есть отметка о согласии':'Нет отметки о согласии'}</span></div><div class="detail-heading">События</div><div class="timeline">${c.events.length ? c.events.map(event => `<div class="timeline-item"><strong>${esc(eventNames[event.kind]||event.kind)} · ${esc(event.tenant)}${event.amount?' · '+money(event.amount):''}</strong><small>${date(event.date)} · ${esc(event.source)}</small></div>`).join('') : '<p class="empty">Событий пока нет.</p>'}</div><div class="detail-heading">Источники профиля</div>${c.sources.map(source => `<div class="source-row"><strong>${esc(source.tenant)}</strong> · ID ${esc(source.external_id)} · ${esc(source.name||'Имя не указано')}</div>`).join('')}`;
    $('#detail-dialog').showModal();
  } catch (error) { toast(error.message,true); }
}
async function findSegment() {
  try {
    const source = $('#source-select').value;
    const target = $('#target-select').value;
    if (source === target) { toast('Выберите два разных проекта',true); return; }
    const segment = await api(`/api/segments?source=${source}&target=${target}`);
    state.segment = segment;
    const destination = state.tenants.find(tenant => tenant.id == target);
    $('#segment-result').classList.remove('hidden');
    $('#segment-result').innerHTML = `<div class="segment-summary"><span class="segment-number">${segment.count}</span><div><strong>Клиентов для приглашения</strong><small>Были в первом проекте. Покупки во втором проекте нет в полученных данных.</small></div></div>${segment.count ? `<div class="segment-people">${segment.profiles.slice(0,8).map(profile => `<span class="person-chip">${esc(profile.name)}</span>`).join('')}${segment.count>8?`<span class="person-chip">+${segment.count-8}</span>`:''}</div><div class="segment-actions"><input id="campaign-name" aria-label="Название кампании" placeholder="Название кампании" value="Приглашение в ${esc(destination.name)}"><button class="primary-button" id="create-campaign">Сохранить кампанию</button></div>` : '<p class="empty">Подходящих клиентов пока нет. Попробуйте другую пару проектов.</p>'}`;
  } catch (error) { toast(error.message,true); }
}
async function createCampaign() {
  try {
    const segment = state.segment;
    const campaign = await api('/api/campaigns',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:$('#campaign-name').value,source:segment.source,target:segment.target,window_days:14})});
    toast(`Кампания сохранена: ${campaign.members} клиентов`);
    $('#segment-result').innerHTML = `<div class="segment-summary"><span class="segment-number">✓</span><div><strong>Кампания сохранена</strong><small>Скачайте CSV и используйте его в привычном сервисе рассылок.</small></div></div><div class="segment-actions" style="margin-top:18px"><a class="primary-button" href="/api/campaigns/${campaign.id}/export.csv">Скачать аудиторию CSV</a></div>`;
    await loadCampaigns();
  } catch (error) { toast(error.message,true); }
}
async function loadCampaigns() {
  const campaigns = await api('/api/campaigns');
  $('#campaigns-table').innerHTML = campaigns.length ? campaigns.map(c => `<tr><td data-label="Кампания"><strong>${esc(c.name)}</strong></td><td data-label="Маршрут">${esc(c.source)} → ${esc(c.target)}</td><td data-label="Клиенты">${c.members}</td><td data-label="Первые покупки"><span class="tag">${c.converted}</span></td><td data-label="Дата">${date(c.date)}</td><td data-label=""><a class="row-btn" href="/api/campaigns/${c.id}/export.csv">Скачать CSV</a></td></tr>`).join('') : '<tr><td colspan="6" class="empty">Кампаний пока нет. Найдите аудиторию выше.</td></tr>';
}
document.addEventListener('click',event => {
  const nav = event.target.closest('[data-page]'); if (nav) showPage(nav.dataset.page);
  const go = event.target.closest('[data-goto]'); if (go) showPage(go.dataset.goto);
  const customer = event.target.closest('[data-customer]'); if (customer) openCustomer(customer.dataset.customer);
  if (event.target.id === 'create-campaign') createCampaign();
});
$('#close-dialog').addEventListener('click',() => $('#detail-dialog').close());
$('#detail-dialog').addEventListener('click',event => {if (event.target.id === 'detail-dialog') event.target.close();});
$('#find-segment').addEventListener('click',findSegment);
$('#refresh-overview').addEventListener('click',refresh);
let searchTimer;
$('#customer-search').addEventListener('input',() => {clearTimeout(searchTimer);searchTimer=setTimeout(loadCustomers,220);});
loadTenants().then(refresh).catch(error => toast(error.message,true));
