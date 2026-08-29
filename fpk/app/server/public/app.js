import { escapeHtml as esc, formatBytes as fmtBytes, formatRate as fmtRate, formatTime as fmtTime, normalizeSubscriptionInfo } from './lib/view-utils.js';
import { createPageLifecycle, isAbortError } from './lib/page-lifecycle.js';

const PREFIX = location.pathname.startsWith('/app/clash-for-fnos') ? '/app/clash-for-fnos' : '';
const api = (p, options={}) => fetch(`${PREFIX}${p}`, options).then(async r => {
  const ct = r.headers.get('content-type') || '';
  const data = ct.includes('json') ? await r.json() : await r.text();
  if (!r.ok) throw new Error(data?.error || data?.message || data || `HTTP ${r.status}`);
  return data;
});

const pages = {
  dashboard: ['◫','仪表盘','查看 Mihomo 核心状态与实时流量'],
  proxies: ['◈','代理节点','切换策略组节点并查看延迟'],
  profiles: ['⇄','订阅配置','管理远程订阅与 NAS 本机配置'],
  config: ['⌘','配置文件','查看当前 Mihomo 本机生效 YAML'],
  rules: ['◇','规则','查看并更新当前配置的 Rule Providers'],
  connections: ['⇆','连接','查看并关闭当前网络连接'],
  logs: ['≡','日志','实时查看 Mihomo 运行日志'],
  settings: ['⚙','设置','设置分类与自动保存']
};
let current = 'dashboard';
let trafficES = null, logES = null;
let proxyCache = null;
let proxySearchText = '';
let settingsView = 'home';
let profileRenderSeq = 0;
const pageLifecycle = createPageLifecycle();
const expandedProxyGroups = new Set();
const proxyLatency = new Map();
const proxyLatencyState = new Map();
const MODE_LABELS = { rule: '规则', global: '全局', direct: '直连' };

// Keep app icon switching scoped to Clash for fnOS itself.
// Do not mutate the parent fnOS desktop DOM: doing so can accidentally replace other apps' icons.
function syncFnosWindowBrand(iconId){
  const id=String(iconId||'').trim();
  if(!/^[a-z0-9][a-z0-9-]{0,63}$/.test(id))return;
  const href=`${location.origin}${PREFIX}/icons/${id}_64.png?v=${Date.now()}`;
  let link=document.querySelector('link[rel~="icon"]');
  if(!link){link=document.createElement('link');link.rel='icon';link.type='image/png';document.head.appendChild(link)}
  link.href=href;
}

function scheduleFnosWindowBrand(iconId){
  syncFnosWindowBrand(iconId);
}

const THEME_KEY = 'clash-for-fnos-theme';
const FNOS_THEME_KEY = 'DesktopConfig-1000';
const FNOS_LEGACY_THEME_KEY = 'fnos-theme-mode';
const themeMedia = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;
let fnosThemeSignature = '';

function browserSystemTheme(){
  return themeMedia?.matches ? 'dark' : 'light';
}
function parentFnosTheme(){
  try{
    if(window.parent && window.parent !== window){
      const doc = window.parent.document;
      const body = doc.body;
      const root = doc.documentElement;
      const attr = String(body?.getAttribute('theme-mode') || root?.getAttribute('theme-mode') || root?.dataset?.theme || '').toLowerCase();
      if(attr === 'dark' || attr === 'light') return attr;
      if(root?.classList?.contains('dark')) return 'dark';
    }
  }catch(_){}
  return null;
}
function readFnosTheme(){
  try{
    const raw = localStorage.getItem(FNOS_THEME_KEY);
    if(raw){
      const value = Number(JSON.parse(raw)?.userPreference?.theme);
      if(value === 10) return {mode:'light', resolved:'light', source:'DesktopConfig-1000'};
      if(value === 20) return {mode:'dark', resolved:'dark', source:'DesktopConfig-1000'};
      if(value === 30) return {mode:'system', resolved:browserSystemTheme(), source:'DesktopConfig-1000'};
    }
  }catch(_){}
  try{
    const legacy = String(localStorage.getItem(FNOS_LEGACY_THEME_KEY) || '').toLowerCase();
    if(legacy === 'dark' || legacy === '20') return {mode:'dark', resolved:'dark', source:'fnos-theme-mode'};
    if(legacy === 'light' || legacy === '10') return {mode:'light', resolved:'light', source:'fnos-theme-mode'};
    if(legacy === 'system' || legacy === '30') return {mode:'system', resolved:browserSystemTheme(), source:'fnos-theme-mode'};
  }catch(_){}
  const parent = parentFnosTheme();
  if(parent) return {mode:parent, resolved:parent, source:'fnos-parent'};
  return {mode:'system', resolved:browserSystemTheme(), source:'browser'};
}
function applySystemTheme(force=false){
  const state = readFnosTheme();
  const signature = `${state.mode}:${state.resolved}:${state.source}`;
  if(!force && signature === fnosThemeSignature) return;
  fnosThemeSignature = signature;
  document.documentElement.dataset.themeMode='system';
  document.documentElement.dataset.fnosThemeMode=state.mode;
  document.documentElement.dataset.theme=state.resolved;
  document.documentElement.style.colorScheme=state.resolved;
  try{localStorage.setItem(THEME_KEY,'system')}catch(_){}
}
applySystemTheme(true);
window.addEventListener('storage', event => {
  if(event.key === FNOS_THEME_KEY || event.key === FNOS_LEGACY_THEME_KEY) applySystemTheme(true);
});
if(themeMedia){
  const onSystemThemeChange=()=>applySystemTheme(true);
  if(themeMedia.addEventListener)themeMedia.addEventListener('change',onSystemThemeChange);else if(themeMedia.addListener)themeMedia.addListener(onSystemThemeChange);
}
window.setInterval(()=>applySystemTheme(false),500);

const qs = s => document.querySelector(s);
function subscriptionInfoMarkup(raw){
  const s=normalizeSubscriptionInfo(raw);if(!s)return '';
  const used=s.upload+s.download;
  const remain=s.total>0?Math.max(0,s.total-used):0;
  const pct=s.total>0?Math.max(0,Math.min(100,used/s.total*100)):0;
  let expireText='长期有效',expireClass='';
  if(s.expire>0){
    const ms=s.expire>1e12?s.expire:s.expire*1000;
    const days=Math.ceil((ms-Date.now())/86400000);
    expireText=days<0?'已过期':days===0?'今天到期':`${new Date(ms).toLocaleDateString()} · 剩余 ${days} 天`;
    if(days<0)expireClass='bad';else if(days<=7)expireClass='warn';
  }
  return `<div class="subscription-info">
    ${s.total>0?`<div class="quota-line"><span>已用 ${fmtBytes(used)}</span><span>剩余 <strong>${fmtBytes(remain)}</strong> / ${fmtBytes(s.total)}</span></div><div class="quota-track"><span data-progress-width="${pct.toFixed(1)}"></span></div>`:''}
    <div class="quota-expire ${expireClass}">${esc(expireText)}</div>
  </div>`;
}

function applyProgressWidths(root=document){
  root.querySelectorAll('[data-progress-width]').forEach(el=>{
    const value=Math.max(0,Math.min(100,Number(el.dataset.progressWidth)||0));
    el.style.width=`${value}%`;
    el.removeAttribute('data-progress-width');
  });
}

function lastDownloadMarkup(info){
  if(!info||typeof info!=='object')return '';
  const attempts=Array.isArray(info.attempts)?info.attempts:[];
  const summary=attempts.map(a=>{
    if(a.skipped)return `${a.label}: 跳过`;
    if(a.status)return `${a.label}: HTTP ${a.status}`;
    return `${a.label}: ${a.error||'失败'}`;
  }).join('；');
  const ok=info.method&&info.method!=='failed'&&Number(info.status)>=200&&Number(info.status)<300;
  const main=ok?`${info.label||info.method} · HTTP ${info.status} · ${Number(info.durationMs||0)} ms`:`${info.label||'更新失败'}${summary?` · ${summary}`:''}`;
  return `<div class="download-info ${ok?'good':'bad'}" title="${esc(summary)}">最近下载：${esc(main)}</div>`;
}
function providerUpdatedText(v){
  const raw=String(v||'');
  if(!raw||/^0001-01-01/i.test(raw))return '--';
  return raw;
}
function toast(msg,bad=false){const el=document.createElement('div');el.className=`toast ${bad?'bad':''}`;el.textContent=msg;qs('#toast').append(el);setTimeout(()=>el.remove(),3200)}
function busy(btn,on=true){if(!btn)return;btn.disabled=on;if(on){btn.dataset.old=btn.textContent;btn.textContent='处理中…'}else{btn.textContent=btn.dataset.old||btn.textContent}}
const settingsAutoSaveState={
  network:{timer:null,running:false,pending:null,pendingJson:'',lastJson:''},
  behavior:{timer:null,running:false,pending:null,pendingJson:'',lastJson:''}
};
const proxyEnvironmentAutoSaveState={timer:null,running:false,pending:null,pendingJson:'',lastJson:''};
function queueSettingsAutoSave(kind,payload,delay=350){
  const st=settingsAutoSaveState[kind];if(!st)return;
  const json=JSON.stringify(payload);
  if(json===st.pendingJson||(!st.running&&json===st.lastJson))return;
  st.pending=payload;st.pendingJson=json;
  if(st.timer)clearTimeout(st.timer);
  st.timer=setTimeout(()=>flushSettingsAutoSave(kind),Math.max(0,Number(delay)||0));
}
async function flushSettingsAutoSave(kind){
  const st=settingsAutoSaveState[kind];if(!st)return;
  st.timer=null;
  if(st.running||!st.pending)return;
  const payload=st.pending,json=st.pendingJson;st.pending=null;st.pendingJson='';st.running=true;
  try{
    const endpoint=kind==='network'?'/api/network/settings':'/api/settings';
    await api(endpoint,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
    st.lastJson=json;
    if(kind==='network')coreHealth().catch(()=>{});
  }catch(e){toast(`自动保存失败：${e.message}`,true)}
  finally{
    st.running=false;
    if(st.pending){clearTimeout(st.timer);st.timer=setTimeout(()=>flushSettingsAutoSave(kind),180)}
  }
}
function queueProxyEnvironmentAutoSave(payload,delay=260){
  const st=proxyEnvironmentAutoSaveState;
  const json=JSON.stringify(payload);
  if(json===st.pendingJson||(!st.running&&json===st.lastJson))return;
  st.pending=payload;st.pendingJson=json;
  if(st.timer)clearTimeout(st.timer);
  st.timer=setTimeout(flushProxyEnvironmentAutoSave,Math.max(0,Number(delay)||0));
}
async function flushProxyEnvironmentAutoSave(){
  const st=proxyEnvironmentAutoSaveState;
  st.timer=null;
  if(st.running||!st.pending)return;
  const payload=st.pending,json=st.pendingJson;st.pending=null;st.pendingJson='';st.running=true;
  try{
    const result=await api('/api/system/proxy-environment',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
    st.lastJson=json;
    const changed=result?.operation?.changed||[];
    const active=result?.management?.active===true;
    const enabled=result?.management?.settings?.enabled===true;
    if(changed.length)toast(active?`代理环境变量已更新：${changed.length} 个系统文件`:enabled?'代理环境变量已保存，等待 Mixed Port 启用':'代理环境变量已关闭');
    if(current==='settings'&&settingsView==='advanced')setTimeout(()=>renderSettings().catch(()=>{}),120);
  }catch(e){toast(`代理环境变量保存失败：${e.message}`,true)}
  finally{
    st.running=false;
    if(st.pending){clearTimeout(st.timer);st.timer=setTimeout(flushProxyEnvironmentAutoSave,180)}
  }
}
function modal(html){qs('#modalBody').innerHTML=html;qs('#modal').classList.remove('hidden')}
function closeModal(){ qs('#modal').classList.add('hidden'); }
qs('#modal').addEventListener('click',e=>{if(e.target.id==='modal')closeModal()});
qs('#modal').addEventListener('click',e=>{if(e.target.closest?.('[data-modal-close]'))closeModal()});

function buildNav(){
  qs('#nav').innerHTML=Object.entries(pages).map(([k,v])=>`<a href="#${k}" class="nav-item ${k===current?'active':''}" data-page="${k}"><span class="nav-icon">${v[0]}</span><span>${v[1]}</span></a>`).join('');
}
async function setPage(name){
  if(!pages[name])name='dashboard';
  const page=pageLifecycle.begin(name);
  current=name; buildNav();
  qs('#pageTitle').textContent=pages[name][1]; qs('#pageDesc').textContent=pages[name][2];
  qs('#pageActions').innerHTML='';
  qs('#content').className='content';
  stopStreams();
  const fn={dashboard:renderDashboard,proxies:renderProxies,profiles:renderProfiles,config:renderConfig,rules:renderRules,connections:renderConnections,logs:renderLogs,settings:renderSettings}[name];
  qs('#content').innerHTML='<div class="empty">加载中…</div>';
  try{await fn(page)}catch(e){if(!page.isCurrent()||isAbortError(e))return;qs('#content').innerHTML=`<div class="empty error-text">${esc(e.message)}</div>`;toast(e.message,true)}
}
window.addEventListener('hashchange',()=>setPage(location.hash.slice(1)));
qs('#refreshBtn').addEventListener('click',()=>setPage(current));
function stopStreams(){if(trafficES){trafficES.close();trafficES=null}if(logES){logES.close();logES=null}}

async function coreHealth(){
  try{const s=await api('/api/status');qs('#coreDot').className='core-dot online';qs('#coreState').textContent='已连接';qs('#coreVersion').textContent=s.version?.version||'Mihomo';return {...s,online:true}}
  catch(e){
    try{
      const sys=await api('/api/system/status'),b=sys.bootstrap||{};
      const working=['checking','downloading','installing','starting'].includes(b.state);
      qs('#coreDot').className=`core-dot ${working?'working':'offline'}`;
      if(working){qs('#coreState').textContent='正在准备 Core';qs('#coreVersion').textContent=`${b.progress||0}% · ${b.message||'处理中'}`;return {online:false,system:sys,bootstrap:b,error:e.message}}
      if(b.state==='error'){qs('#coreState').textContent='Core 安装失败';qs('#coreVersion').textContent='点击仪表盘重试';return {online:false,system:sys,bootstrap:b,error:b.error||e.message}}
      if(b.state==='external-stopped'){qs('#coreState').textContent='外部 Core 未运行';qs('#coreVersion').textContent='已检测到本机安装';return {online:false,system:sys,bootstrap:b,error:b.message||e.message}}
    }catch(_){}
    qs('#coreDot').className='core-dot offline';qs('#coreState').textContent='未连接';qs('#coreVersion').textContent='检查设置';throw e
  }
}

async function renderDashboard(){
  const s=await coreHealth();
  if(!s.online){
    const b=s.bootstrap||{};const working=['checking','downloading','installing','starting'].includes(b.state);
    const deliveryHint=b.delivery==='online'?'当前是 all 通用安装包，不包含 Mihomo Core；Manager 会识别 CPU 架构，从 MetaCubeX/mihomo 官方 GitHub Release 下载匹配资产，校验 SHA-256 后安装。首次启用需要访问 GitHub。':'当前架构安装包已内置官方 Mihomo Core；未检测到本机 Core 时会本地校验 SHA-256 后直接启用，无需联网下载或 SSH 安装。';
    qs('#content').innerHTML=`<div class="card section bootstrap-card"><div class="section-head"><div><h2>${working?'正在准备 Mihomo Core':b.state==='error'?'Mihomo Core 启用失败':'Mihomo Core 尚未运行'}</h2><p>${esc(b.message||s.error||'Manager 正在检查本机 Mihomo')}</p></div>${b.mode?`<span class="tag">${esc(b.mode==='managed'?'Manager 托管':'外部 Core')}</span>`:''}</div>${working?`<div class="bootstrap-progress"><span data-progress-width="${Math.max(3,Math.min(100,Number(b.progress||0)))}"></span></div><div class="hint">${esc(deliveryHint)}</div>`:`<div class="local-warning">${esc(b.error||s.error||b.message||'未检测到可用 Core')}</div><div class="actions" style="margin-top:14px"><button id="retryBootstrap">重新检测并安装</button><button class="ghost" id="openSettings">打开设置</button></div>`}</div>`;
    applyProgressWidths(qs('#content'));
    if(qs('#retryBootstrap'))qs('#retryBootstrap').onclick=async()=>{const btn=qs('#retryBootstrap');busy(btn);try{await api('/api/core/bootstrap/retry',{method:'POST'});toast('Mihomo Core 已准备完成');setTimeout(()=>renderDashboard(),500)}catch(e){toast(e.message,true);renderDashboard()}finally{busy(btn,false)}};
    if(qs('#openSettings'))qs('#openSettings').onclick=()=>{location.hash='settings'};
    if(working)setTimeout(()=>{if(current==='dashboard')renderDashboard().catch(()=>{})},1500);
    return;
  }
  const c=s.configs||{}, conn=s.connections||{};
  qs('#content').innerHTML=`
    <div class="grid stats">
      <div class="card stat"><div class="label">核心版本</div><div class="value" style="font-size:20px">${esc(s.version?.version||'-')}</div><div class="sub">External Controller 在线</div></div>
      <div class="card stat"><div class="label">活动连接</div><div class="value" id="connCount">${conn.count||0}</div><div class="sub">实时连接数量</div></div>
      <div class="card stat"><div class="label">累计下载</div><div class="value" id="downTotal">${fmtBytes(conn.downloadTotal)}</div><div class="sub">核心启动以来</div></div>
      <div class="card stat"><div class="label">内存</div><div class="value">${fmtBytes(conn.memory)}</div><div class="sub">Mihomo 当前占用</div></div>
    </div>
    <div class="card section"><div class="section-head"><div><h2>运行模式</h2><p>无需修改 YAML，可直接热切换</p></div><div class="mode-row">${['rule','global','direct'].map(x=>`<button class="mode-btn ${String(c.mode).toLowerCase()===x?'active':''}" data-mode="${x}">${MODE_LABELS[x]||x}</button>`).join('')}</div></div></div>
    <div class="card section"><div class="section-head"><div><h2>实时流量</h2><p>数据来自 Mihomo /traffic</p></div></div><div class="traffic-wrap"><div class="meter"><span class="muted">上传</span><div class="big up" id="upRate">0 B/s</div><small class="muted" id="upTotal">累计 ${fmtBytes(conn.uploadTotal)}</small></div><div class="meter"><span class="muted">下载</span><div class="big down" id="downRate">0 B/s</div><small class="muted" id="downTotal2">累计 ${fmtBytes(conn.downloadTotal)}</small></div></div></div>
    <div class="card section"><div class="section-head"><div><h2>端口与网络</h2><p>当前运行配置摘要</p></div></div><div class="table-wrap"><table><tbody>
      <tr><td class="muted">Mixed Port</td><td class="mono">${esc(c['mixed-port']??'-')}</td><td class="muted">Allow LAN</td><td>${c['allow-lan']?'开启':'关闭'}</td></tr>
      <tr><td class="muted">HTTP Port</td><td class="mono">${esc(c.port??'-')}</td><td class="muted">SOCKS Port</td><td class="mono">${esc(c['socks-port']??'-')}</td></tr>
      <tr><td class="muted">IPv6</td><td>${c.ipv6?'开启':'关闭'}</td><td class="muted">TUN</td><td>${c.tun?.enable?'开启':'关闭'}</td></tr>
    </tbody></table></div></div>`;
  document.querySelectorAll('[data-mode]').forEach(b=>b.onclick=async()=>{try{await api('/api/runtime-config',{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({mode:b.dataset.mode})});toast(`已切换到 ${MODE_LABELS[b.dataset.mode]||b.dataset.mode}`);renderDashboard()}catch(e){toast(e.message,true)}});
  startTraffic();
}
function startTraffic(){
  trafficES=new EventSource(`${PREFIX}/api/stream/traffic`);
  trafficES.onmessage=e=>{try{const d=JSON.parse(e.data);if(qs('#upRate'))qs('#upRate').textContent=fmtRate(d.up);if(qs('#downRate'))qs('#downRate').textContent=fmtRate(d.down);if(qs('#upTotal'))qs('#upTotal').textContent=`累计 ${fmtBytes(d.upTotal)}`;if(qs('#downTotal2'))qs('#downTotal2').textContent=`累计 ${fmtBytes(d.downTotal)}`;}catch(_){}};
}

function latencyOf(p){const h=Array.isArray(p?.history)?p.history:[];const last=h[h.length-1];return Number(last?.delay||0)}
function latencyClass(n){return !n?'':n<100?'good':n<250?'warn':'bad'}
function latencySnapshot(name){
  const state=proxyLatencyState.get(name);
  const delay=Number(proxyLatency.get(name)||0);
  if(state==='testing')return {text:'测速中…',cls:'testing'};
  if(state==='timeout')return {text:'超时',cls:'bad'};
  if(state==='error')return {text:'失败',cls:'bad'};
  if(delay>0)return {text:`${delay} ms`,cls:latencyClass(delay)};
  return {text:'--',cls:''};
}
function seedProxyLatency(){
  for(const [name,p] of Object.entries(proxyCache||{})){
    const d=latencyOf(p);
    if(d>0&&!proxyLatency.has(name))proxyLatency.set(name,d);
  }
}
function isDelayTestable(name){
  const p=proxyCache?.[name]||{};
  const type=String(p.type||'').toLowerCase();
  return !['direct','reject','pass'].includes(type)&&!['DIRECT','REJECT','PASS'].includes(String(name));
}
function updateLatencyDom(name){
  const key=encodeURIComponent(name);
  const snap=latencySnapshot(name);
  document.querySelectorAll(`[data-delay-key="${key}"]`).forEach(el=>{
    el.textContent=snap.text;
    el.className=`node-delay ${snap.cls}`;
  });
  document.querySelectorAll(`[data-current-delay-key="${key}"]`).forEach(el=>{
    el.textContent=snap.text==='--'?'':snap.text;
    el.className=`current-delay ${snap.cls} ${snap.text==='--'?'hidden':''}`;
  });
}
async function testProxyNode(name){
  if(!isDelayTestable(name)){
    proxyLatency.delete(name);proxyLatencyState.delete(name);updateLatencyDom(name);return;
  }
  proxyLatencyState.set(name,'testing');updateLatencyDom(name);
  try{
    const d=await api(`/api/delay/${encodeURIComponent(name)}`);
    const delay=Number(d?.delay||0);
    if(delay>0){proxyLatency.set(name,delay);proxyLatencyState.set(name,'done')}
    else{proxyLatency.delete(name);proxyLatencyState.set(name,'error')}
  }catch(e){
    proxyLatency.delete(name);
    proxyLatencyState.set(name,/timeout|超时|abort/i.test(String(e?.message||''))?'timeout':'error');
  }
  updateLatencyDom(name);
}
async function testProxyNodes(names,{button=null,label='测速完成'}={}){
  const unique=[...new Set(names)].filter(isDelayTestable);
  if(!unique.length){toast('没有可测速节点');return}
  if(button){button.disabled=true;button.dataset.old=button.textContent;button.textContent=`测速中 0/${unique.length}`}
  unique.forEach(name=>{proxyLatencyState.set(name,'testing');updateLatencyDom(name)});
  let cursor=0,done=0;
  const worker=async()=>{
    while(true){
      const i=cursor++; if(i>=unique.length)return;
      const name=unique[i];
      try{
        const d=await api(`/api/delay/${encodeURIComponent(name)}`);
        const delay=Number(d?.delay||0);
        if(delay>0){proxyLatency.set(name,delay);proxyLatencyState.set(name,'done')}
        else{proxyLatency.delete(name);proxyLatencyState.set(name,'error')}
      }catch(e){
        proxyLatency.delete(name);
        proxyLatencyState.set(name,/timeout|超时|abort/i.test(String(e?.message||''))?'timeout':'error');
      }
      done++;updateLatencyDom(name);
      if(button)button.textContent=`测速中 ${done}/${unique.length}`;
    }
  };
  await Promise.all(Array.from({length:Math.min(6,unique.length)},worker));
  if(button){button.disabled=false;button.textContent=button.dataset.old||'测速'}
  toast(label);
}
function proxyNodeMarkup(group,p,name){
  const active=p.now===name;
  const key=encodeURIComponent(name);
  const snap=latencySnapshot(name);
  const np=proxyCache?.[name]||{};
  const type=String(np.type||'');
  return `<div class="node-row ${active?'active':''}" data-node-search="${encodeURIComponent(String(name).toLowerCase())}" title="${esc(name)}">
    <button class="node-select" data-group="${encodeURIComponent(group)}" data-node="${key}">
      <span class="node-name">${esc(name)}</span>
      ${type?`<span class="node-type">${esc(type)}</span>`:''}
      ${active?'<span class="node-selected-mark">当前</span>':''}
    </button>
    <button class="node-delay ${snap.cls}" data-test-node="${key}" data-delay-key="${key}" title="单独测试该节点延迟">${snap.text}</button>
  </div>`;
}
async function renderProxies(){
  const data=await api('/api/proxies'); proxyCache=data.proxies||{}; seedProxyLatency();
  // p.all is Mihomo's original selector member array. Do not sort it: this preserves member order.
  // /proxies is a map, so its object-key order is not the YAML proxy-groups order.
  // The backend returns groupOrder parsed from the active startup config (with managed-config fallback).
  const groupEntries=Object.entries(proxyCache).filter(([,p])=>Array.isArray(p.all)&&p.all.length);
  const groupMap=new Map(groupEntries);
  const seenGroups=new Set();
  const groups=[];
  for(const name of (Array.isArray(data.groupOrder)?data.groupOrder:[])){
    if(groupMap.has(name)&&!seenGroups.has(name)){groups.push([name,groupMap.get(name)]);seenGroups.add(name)}
  }
  for(const pair of groupEntries){if(!seenGroups.has(pair[0])){groups.push(pair);seenGroups.add(pair[0])}}
  const allNodes=[...new Set(groups.flatMap(([,p])=>p.all))];
  const orderHint=data.groupOrderSource==='startup'?'卡片按当前启动配置顺序':data.groupOrderSource==='managed'?'卡片按 Manager 配置顺序':'卡片按 Mihomo 接口顺序';
  qs('#content').innerHTML=`<div class="section-head proxy-page-head"><div><h2>代理组</h2><p>${groups.length} 个策略组 · ${orderHint} · 组内节点按配置顺序</p></div><div class="actions proxy-toolbar"><div class="proxy-search"><span>⌕</span><input id="proxySearch" value="${esc(proxySearchText)}" placeholder="搜索代理组或节点" autocomplete="off"><button class="search-clear ${proxySearchText?'':'hidden'}" id="clearProxySearch" title="清空搜索">×</button></div><span class="search-result" id="proxySearchResult"></span><button class="ghost" id="expandAll">全部展开</button><button class="ghost" id="delayAll">全部测速</button></div></div>
    <div class="proxy-groups">${groups.map(([name,p])=>{
      const expanded=expandedProxyGroups.has(name);
      const nowKey=encodeURIComponent(p.now||'');
      const currentSnap=latencySnapshot(p.now||'');
      return `<section class="card proxy-card ${expanded?'expanded':''}" data-proxy-card="${encodeURIComponent(name)}" data-group-search="${encodeURIComponent(String(name).toLowerCase())}">
        <div class="proxy-card-head" data-toggle-group="${encodeURIComponent(name)}">
          <div class="proxy-summary">
            <div class="proxy-name-row"><h3>${esc(name)}</h3><span class="tag">${esc(p.type||'Selector')}</span></div>
            <div class="proxy-current"><span>当前</span><strong>${esc(p.now||'-')}</strong><span class="current-delay ${currentSnap.cls} ${currentSnap.text==='--'?'hidden':''}" data-current-delay-key="${nowKey}">${currentSnap.text==='--'?'':currentSnap.text}</span></div>
          </div>
          <div class="proxy-head-actions">
            <button class="proxy-test ghost small" data-test-group="${encodeURIComponent(name)}">测速</button>
            <span class="node-count">${p.all.length}</span>
            <span class="proxy-chevron">⌄</span>
          </div>
        </div>
        <div class="proxy-body"><div class="node-list">${p.all.map(n=>proxyNodeMarkup(name,p,n)).join('')}</div></div>
      </section>`;
    }).join('')}</div>`;

  const applySearch=()=>{
    const q=String(proxySearchText||'').trim().toLowerCase();
    let visibleGroups=0,visibleNodes=0;
    document.querySelectorAll('.proxy-card').forEach(card=>{
      const groupName=decodeURIComponent(card.dataset.groupSearch||'');
      const groupMatch=!q||groupName.includes(q);
      let matchedNodes=0,totalNodes=0;
      card.querySelectorAll('.node-row').forEach(row=>{
        totalNodes++;
        const nodeName=decodeURIComponent(row.dataset.nodeSearch||'');
        const match=!q||groupMatch||nodeName.includes(q);
        row.hidden=!match;
        if(match)matchedNodes++;
      });
      const visible=!q||groupMatch||matchedNodes>0;
      card.hidden=!visible;
      card.classList.toggle('search-expanded',Boolean(q&&visible));
      if(visible){visibleGroups++;visibleNodes+=groupMatch?totalNodes:matchedNodes}
    });
    const result=qs('#proxySearchResult');
    if(result)result.textContent=q?`${visibleGroups} 组 · ${visibleNodes} 个节点`:'';
    const clear=qs('#clearProxySearch');if(clear)clear.classList.toggle('hidden',!q);
  };

  document.querySelectorAll('[data-toggle-group]').forEach(head=>head.onclick=e=>{
    if(e.target.closest('button'))return;
    const name=decodeURIComponent(head.dataset.toggleGroup);
    expandedProxyGroups.has(name)?expandedProxyGroups.delete(name):expandedProxyGroups.add(name);
    const card=head.closest('.proxy-card');card.classList.toggle('expanded',expandedProxyGroups.has(name));
  });
  document.querySelectorAll('.node-select').forEach(b=>b.onclick=async()=>{
    const group=decodeURIComponent(b.dataset.group),node=decodeURIComponent(b.dataset.node);
    b.disabled=true;b.classList.add('switching');
    try{await api(`/api/proxies/${encodeURIComponent(group)}`,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:node})});toast(`已切换到 ${node}`);await renderProxies()}
    catch(e){toast(e.message,true)}
    finally{b.disabled=false;b.classList.remove('switching')}
  });
  document.querySelectorAll('[data-test-node]').forEach(b=>b.onclick=async e=>{e.stopPropagation();await testProxyNode(decodeURIComponent(b.dataset.testNode))});
  document.querySelectorAll('[data-test-group]').forEach(b=>b.onclick=async e=>{
    e.stopPropagation();const name=decodeURIComponent(b.dataset.testGroup);const p=proxyCache[name];
    await testProxyNodes(p?.all||[],{button:b,label:`${name} 测速完成`});
  });
  qs('#delayAll').onclick=async()=>testProxyNodes(allNodes,{button:qs('#delayAll'),label:'全部节点测速完成'});
  qs('#expandAll').onclick=()=>{
    const expand=expandedProxyGroups.size!==groups.length;
    expandedProxyGroups.clear();if(expand)groups.forEach(([name])=>expandedProxyGroups.add(name));
    document.querySelectorAll('.proxy-card').forEach(card=>card.classList.toggle('expanded',expand));
    qs('#expandAll').textContent=expand?'全部收起':'全部展开';
  };
  qs('#proxySearch').oninput=e=>{proxySearchText=e.target.value;applySearch()};
  qs('#clearProxySearch').onclick=()=>{proxySearchText='';qs('#proxySearch').value='';applySearch();qs('#proxySearch').focus()};
  applySearch();
}

function localProcessMarkup(p){
  const args=(p.args||[]).join(' ');
  return `<div class="local-process"><div class="local-process-dot"></div><div class="local-process-main"><strong>PID ${esc(p.pid)} · ${esc(p.exe||'mihomo')}</strong><div class="mono muted local-process-args" title="${esc(args)}">${esc(args)}</div></div>${p.containerized?'<span class="tag">容器进程</span>':'<span class="tag">主机进程</span>'}</div>`;
}
function localCandidateMarkup(c, importedPaths=new Set()){
  const state=c.readable?'可读取':c.permissionDenied||c.exists?'无读取权限':'未找到';
  const stateCls=c.readable?'good':c.permissionDenied||c.exists?'bad':'muted';
  const imported=importedPaths.has(c.path);
  return `<div class="local-config-row">
    <div class="local-config-icon">Y</div>
    <div class="local-config-main"><div class="local-config-path mono" title="${esc(c.path)}">${esc(c.path)}</div><div class="local-config-meta"><span>${esc(c.source||'检测')}</span>${c.namespace==='process-root'?'<span>进程根目录</span>':''}${c.size?`<span>${fmtBytes(c.size)}</span>`:''}${c.mtime?`<span>${esc(fmtTime(c.mtime))}</span>`:''}</div></div>
    <div class="local-config-state ${stateCls}">${imported?'<span class="local-imported">已导入</span> ':''}${esc(state)}</div>
    <div class="actions local-config-actions">${c.readable&&c.token?`<button class="ghost small" data-local-import="${esc(c.token)}">导入</button><button class="success small" data-local-apply="${esc(c.token)}">导入并应用</button>`:`<button class="ghost small" disabled>${esc(state)}</button>`}</div>
  </div>`;
}
async function importNasLocal(token,apply,btn){
  busy(btn);
  try{
    const r=await api('/api/local-config/import',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token,apply})});
    toast(apply?`已导入并应用：${r.sourcePath}`:`已导入：${r.sourcePath}`);
    closeModal();
    await renderProfiles();
  }catch(e){toast(e.message,true)}finally{busy(btn,false)}
}

function remoteProfileFormMarkup(prefix,item=null){
  const isEdit=Boolean(item);
  const interval=item?.intervalMinutes ?? 360;
  const autoUpdate=isEdit?Boolean(item.autoUpdate):true;
  const autoApply=isEdit?Boolean(item.autoApply):false;
  return `<div class="form-grid">
    <div class="field"><label>名称</label><input id="${prefix}Name" value="${esc(item?.name||'')}" placeholder="例如：机场订阅" /></div>
    <div class="field"><label>更新间隔（分钟）</label><input id="${prefix}Interval" type="number" value="${esc(interval)}" min="5" /></div>
    <div class="field full"><label>订阅 URL</label><input id="${prefix}Url" value="${esc(item?.url||'')}" placeholder="https://..." /></div>
    <div class="field full"><div class="hint">自动更新顺序：直连 → 当前 Mihomo mixed-port → 系统 HTTP/HTTPS 代理。</div></div>
    <div class="field"><label><input id="${prefix}Auto" type="checkbox" ${autoUpdate?'checked':''} style="width:auto"> 自动更新</label></div>
    <div class="field"><label><input id="${prefix}Apply" type="checkbox" ${autoApply?'checked':''} style="width:auto"> 当前配置更新后自动应用</label></div>
  </div>`;
}
function readRemoteProfileForm(prefix){
  const name=qs(`#${prefix}Name`)?.value.trim()||'';
  const url=qs(`#${prefix}Url`)?.value.trim()||'';
  const intervalMinutes=Number(qs(`#${prefix}Interval`)?.value);
  if(!name)throw new Error('请输入订阅名称');
  if(!url)throw new Error('请输入订阅 URL');
  if(!Number.isFinite(intervalMinutes)||intervalMinutes<5)throw new Error('更新间隔不能小于 5 分钟');
  return {name,url,intervalMinutes,autoUpdate:Boolean(qs(`#${prefix}Auto`)?.checked),autoApply:Boolean(qs(`#${prefix}Apply`)?.checked)};
}

async function renderProfiles(){
  const renderSeq=++profileRenderSeq;
  const active=()=>current==='profiles'&&renderSeq===profileRenderSeq;

  qs('#content').innerHTML=`
    <div class="card section"><div class="section-head"><div><h2>添加远程订阅</h2><p>下载完整 Mihomo/Clash YAML，应用时自动同步为 Mihomo 启动配置</p></div></div>
      <div class="actions"><button id="openAddProfile">添加订阅</button><button class="ghost" id="importLocal">从当前电脑导入 YAML</button></div>
    </div>
    <div class="card section"><div class="section-head"><div><h2>配置列表</h2><p>本机配置与远程订阅统一管理</p></div></div>
      <div class="profile-list" id="profilesAsync"><div class="async-placeholder"><span class="async-dot"></span>正在后台读取配置列表…</div></div>
    </div>
    <div class="card section local-discovery-card"><div class="section-head"><div><h2>本机 Mihomo 配置</h2><p>自动读取当前 Mihomo 配置；用户文件仅从 fnOS 明确授权的目录读取</p></div><button class="ghost" id="rescanLocal">重新扫描</button></div>
      <div id="localProfilesAsync" class="async-slot"><div class="async-placeholder"><span class="async-dot"></span>正在后台扫描本机配置…</div></div>
    </div>`;

  const openRemoteProfileModal=(item=null)=>{
    const editing=Boolean(item);
    const prefix=editing?'editPf':'addPf';
    modal(`<h3>${editing?'编辑订阅':'添加远程订阅'}</h3><div class="hint" style="margin-bottom:14px">${editing?'修改订阅信息后保存；订阅内容将在下次手动或自动更新时重新下载。':'填写远程订阅信息，添加后会立即尝试下载一次。'}</div>${remoteProfileFormMarkup(prefix,item)}<div class="actions" style="margin-top:16px"><button id="profileModalSubmit">${editing?'保存修改':'添加订阅'}</button><button class="ghost" data-modal-close>取消</button></div>`);
    qs('#profileModalSubmit').onclick=async()=>{
      const btn=qs('#profileModalSubmit');
      try{
        const payload=readRemoteProfileForm(prefix);
        busy(btn);
        if(editing){
          await api(`/api/profiles/${item.id}`,{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
          closeModal();toast('订阅设置已保存');
        }else{
          await api('/api/profiles',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
          closeModal();toast('订阅已添加');
        }
        renderProfiles();
      }catch(e){toast(e.message,true)}finally{busy(btn,false)}
    };
  };

  qs('#rescanLocal').onclick=()=>renderProfiles();
  qs('#openAddProfile').onclick=()=>openRemoteProfileModal();
  qs('#importLocal').onclick=()=>{modal(`<h3>从当前电脑导入 YAML</h3><div class="hint" style="margin-bottom:12px">这里选择的是你正在打开 fnOS 的电脑上的文件；NAS 本机配置请使用页面底部的自动扫描。</div><div class="field"><label>名称</label><input id="localName" value="本地配置"></div><div class="field" style="margin-top:10px"><label>选择文件</label><input id="localFile" type="file" accept=".yaml,.yml,.txt"></div><div class="actions" style="margin-top:16px"><button id="doImport">导入</button><button class="ghost" data-modal-close>取消</button></div>`);qs('#doImport').onclick=async()=>{const f=qs('#localFile').files[0];if(!f)return toast('请选择文件',true);try{await api('/api/profiles/import',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:qs('#localName').value,content:await f.text()})});closeModal();toast('已导入');renderProfiles()}catch(e){toast(e.message,true)}}};

  api('/api/local-config/discover').then(local=>{
    if(!active())return;
    const authorizedPaths=Array.isArray(local.authorizedPaths)?local.authorizedPaths:[];
    const slot=qs('#localProfilesAsync');if(!slot)return;
    slot.innerHTML=`${local.error?`<div class="local-warning">扫描失败：${esc(local.error)}</div>`:''}
      <div class="local-access-summary ${authorizedPaths.length?'active':'warn'}"><strong>${authorizedPaths.length?`已授权 ${authorizedPaths.length} 个文件夹`:'尚未授权用户文件夹'}</strong><span>${authorizedPaths.length?authorizedPaths.map(x=>`<span class="mono">${esc(x)}</span>`).join(''): '如需从 NAS 共享目录导入 YAML，请到 fnOS「系统设置 → 应用 → Clash for fnos → 访问权限」添加文件夹。系统 Mihomo 启动配置仍由特权 helper 独立管理。'}</span></div>
      <div class="local-processes">${(local.processes||[]).length?(local.processes||[]).map(localProcessMarkup).join(''):'<div class="local-process none"><div class="local-process-dot off"></div><div><strong>没有从 /proc 识别到 Mihomo 进程</strong><div class="muted">仍会继续检查常见 config.yaml 路径</div></div></div>'}</div>`;
  }).catch(e=>{
    if(!active())return;
    const slot=qs('#localProfilesAsync');if(slot)slot.innerHTML=`<div class="local-warning">扫描失败：${esc(e.message)}</div>`;
  });

  api('/api/profiles').then(d=>{
    if(!active())return;
    const items=Array.isArray(d.items)?d.items:[];
    const slot=qs('#profilesAsync');if(!slot)return;
    slot.innerHTML=items.length?items.map(x=>`
      <div class="profile ${x.current?'current':''}"><div><div class="profile-name">${x.current?'● ':''}${esc(x.name)}</div><div class="profile-meta">${x.type==='remote'?'远程订阅':'本地配置'} · 更新：${esc(fmtTime(x.updatedAt))}</div>${x.lastError?`<div class="profile-meta error-text">${esc(x.lastError)}</div>`:''}${x.type==='remote'?lastDownloadMarkup(x.lastDownload):''}</div><div>${x.type==='remote'?subscriptionInfoMarkup(x.subscriptionInfo):`<div class="profile-url" title="${esc(x.sourcePath||'')}">${esc(x.sourcePath||'本地导入')}</div>`}</div><div class="actions">${x.type==='remote'?`<button class="ghost small" data-update="${x.id}">更新</button>`:''}<button class="success small" data-activate="${x.id}">应用</button>${x.type==='remote'?`<button class="ghost small" data-edit="${x.id}">编辑</button>`:''}<button class="danger small" data-delete="${x.id}">删除</button></div></div>`).join(''):'<div class="empty">还没有配置</div>';
    applyProgressWidths(slot);

    document.querySelectorAll('[data-edit]').forEach(b=>b.onclick=()=>{const item=items.find(x=>x.id===b.dataset.edit);if(item)openRemoteProfileModal(item)});
    document.querySelectorAll('[data-update]').forEach(b=>b.onclick=async()=>{busy(b);try{const r=await api(`/api/profiles/${b.dataset.update}/update`,{method:'POST'});const dl=r.lastDownload;toast(dl&&dl.label?`订阅更新完成 · ${dl.label} · ${Number(dl.durationMs||0)} ms`:'订阅更新完成');renderProfiles()}catch(e){toast(e.message,true);renderProfiles()}finally{busy(b,false)}});
    document.querySelectorAll('[data-activate]').forEach(b=>b.onclick=async()=>{
      busy(b);
      try{
        const started=await api(`/api/profiles/${b.dataset.activate}/activate`,{method:'POST'});
        const jobId=started.jobId;
        if(!jobId){toast('未获取到应用任务',true);renderProfiles();return}
        const deadline=Date.now()+3*60*1000;
        let job=started;
        while(Date.now()<deadline){
          if(job.state==='done'){toast(`配置已应用并同步到 ${job.result?.target||'启动配置'}`);renderProfiles();return}
          if(job.state==='failed'){toast(job.error||'配置应用失败',true);renderProfiles();return}
          if(b&&b.isConnected)b.textContent=job.message||'应用中…';
          await new Promise(r=>setTimeout(r,1000));
          job=await api(`/api/jobs/${jobId}`);
        }
        toast('配置仍在后台应用，可稍后刷新查看状态');
      }catch(e){toast(e.message,true);renderProfiles()}finally{busy(b,false)}
    });
    document.querySelectorAll('[data-delete]').forEach(b=>b.onclick=async()=>{if(!confirm('确定删除这个配置吗？'))return;try{await api(`/api/profiles/${b.dataset.delete}`,{method:'DELETE'});toast('已删除');renderProfiles()}catch(e){toast(e.message,true)}});
  }).catch(e=>{
    if(!active())return;
    const slot=qs('#profilesAsync');if(slot)slot.innerHTML=`<div class="empty error-text">读取配置列表失败：${esc(e.message)}</div>`;
  });
}

async function renderConfig(){
  const cfg=await api('/api/config/effective');
  const pathText=cfg?.configPath||'未检测到启动配置路径';
  qs('#pageActions').innerHTML='<span class="tag config-readonly-tag">只读</span>';
  qs('#content').classList.add('config-content');
  qs('#content').innerHTML=`<div class="config-workspace"><div class="config-meta"><span class="tag">当前生效</span><span class="mono config-path" title="${esc(pathText)}">${esc(pathText)}</span>${cfg?.pid?`<span class="muted">PID ${esc(cfg.pid)}</span>`:''}</div><div class="config-scroll"><pre class="config-yaml mono" id="editor"></pre></div></div>`;
  qs('#editor').textContent=cfg?.content||'';
}

async function renderRules(){
  const data=await api('/api/rule-providers').catch(e=>({providers:{},error:e.message}));
  const providerMap=data?.providers||{};
  const entries=Object.entries(providerMap).sort(([a],[b])=>a.localeCompare(b));
  qs('#content').innerHTML=`<div class="card section"><div class="section-head"><div><h2>Rule Providers</h2><p>通过 Mihomo Core API 查看和更新当前配置中的 rule-providers</p></div>${entries.length?'<button class="ghost" id="updateAllRuleProviders">全部更新</button>':''}</div>${data?.error?`<div class="local-warning">读取 Rule Providers 失败：${esc(data.error)}</div>`:''}${entries.length?`<div class="profile-list">${entries.map(([name,v])=>`<div class="profile"><div><div class="profile-name">${esc(name)}</div><div class="profile-meta">${esc(v.behavior||v.vehicleType||v.type||'Rule Provider')} · ${Number(v.ruleCount||0)} 条规则</div></div><div><div class="profile-url">${esc(providerUpdatedText(v.updatedAt))}</div></div><div class="actions"><button class="ghost small" data-rule-provider-update="${encodeURIComponent(name)}">更新</button></div></div>`).join('')}</div>`:'<div class="empty">当前配置没有 Rule Provider，或核心不支持该接口</div>'}</div>`;

  const updateOne=async(btn,name)=>{
    busy(btn);
    try{
      const r=await api(`/api/rule-providers/${encodeURIComponent(name)}/update`,{method:'PUT'});
      toast(r?.method==='direct-fallback'?`${name} 常规更新失败，已通过直连兜底更新`:`${name} 更新已触发`);
      setTimeout(()=>{if(current==='rules')renderRules().catch(()=>{})},800);
      return true;
    }catch(e){toast(`${name}: ${e.message}`,true);return false}
    finally{busy(btn,false)}
  };

  document.querySelectorAll('[data-rule-provider-update]').forEach(btn=>btn.onclick=()=>updateOne(btn,decodeURIComponent(btn.dataset.ruleProviderUpdate)));
  if(qs('#updateAllRuleProviders'))qs('#updateAllRuleProviders').onclick=async()=>{
    const btn=qs('#updateAllRuleProviders');busy(btn);
    let ok=0,failed=0,directFallback=0;
    try{
      for(const [name] of entries){
        try{const r=await api(`/api/rule-providers/${encodeURIComponent(name)}/update`,{method:'PUT'});ok++;if(r?.method==='direct-fallback')directFallback++}
        catch(_){failed++}
      }
      if(failed)toast(`Rule Providers 更新完成：成功 ${ok}${directFallback?`（直连兜底 ${directFallback}）`:''}，失败 ${failed}`,true);else toast(`已触发 ${ok} 个 Rule Provider 更新${directFallback?`，其中直连兜底 ${directFallback} 个`:''}`);
      setTimeout(()=>{if(current==='rules')renderRules().catch(()=>{})},800);
    }finally{busy(btn,false)}
  };
}

async function renderConnections(){
  const d=await api('/api/connections'), items=d.connections||[];
  qs('#content').innerHTML=`<div class="section-head"><div><h2>${items.length} 个活动连接</h2><p>累计上传 ${fmtBytes(d.uploadTotal)} · 下载 ${fmtBytes(d.downloadTotal)}</p></div><button class="danger" id="closeAll">关闭全部</button></div>${items.length?`<div class="card table-wrap"><table class="connections-table"><thead><tr><th>目标</th><th>进程</th><th>规则</th><th>代理链</th><th>上传</th><th>下载</th><th></th></tr></thead><tbody>${items.map(x=>{const md=x.metadata||{};const host=String(md.host||'').trim();const ip=String(md.destinationIP||'').trim();const port=String(md.destinationPort||'').trim();const target=host||ip||'-';const endpoint=port&&target!=='-'?`${target}:${port}`:target;const tip=host&&ip&&ip!==host?`${endpoint} · ${ip}${port?`:${port}`:''}`:endpoint;return `<tr><td class="conn-target-cell"><div class="conn-target" title="${esc(tip)}">${esc(endpoint)}</div></td><td>${esc(md.process||'-')}</td><td><span class="tag">${esc(x.rule||'-')}</span><div class="muted" style="font-size:11px">${esc(x.rulePayload||'')}</div></td><td>${esc((x.chains||[]).join(' → '))}</td><td>${fmtBytes(x.upload)}</td><td>${fmtBytes(x.download)}</td><td><button class="iconbtn" data-close="${esc(x.id)}">×</button></td></tr>`}).join('')}</tbody></table></div>`:'<div class="empty">当前没有活动连接</div>'}`;
  qs('#closeAll').onclick=async()=>{if(!confirm('关闭所有当前连接？'))return;try{await api('/api/connections',{method:'DELETE'});toast('已关闭全部连接');renderConnections()}catch(e){toast(e.message,true)}};
  document.querySelectorAll('[data-close]').forEach(b=>b.onclick=async()=>{try{await api(`/api/connections/${encodeURIComponent(b.dataset.close)}`,{method:'DELETE'});renderConnections()}catch(e){toast(e.message,true)}});
}

async function renderLogs(page){
  qs('#pageActions').innerHTML=`<select id="logLevel" class="log-level-select"><option>debug</option><option selected>info</option><option>warning</option><option>error</option></select><button class="ghost" id="clearLogs">清空</button><button id="toggleLogs">停止</button>`;
  qs('#content').classList.add('logs-content');
  qs('#content').innerHTML=`<div class="logs logs-full" id="logs"></div>`;
  const displayTime=value=>{if(!value)return new Date().toLocaleTimeString();const d=new Date(value);return Number.isNaN(d.getTime())?String(value):d.toLocaleTimeString()};
  const createLine=d=>{const line=document.createElement('div');const lv=String(d.level||d.type||'info').replace('warning','warn');line.className=`log-line log-${lv}`;line.innerHTML=`<span>${esc(displayTime(d.time))}</span><span>${esc(lv)}</span><span>${esc(d.message||d.payload||'')}</span>`;return line};
  const appendLine=d=>{if(!page.isCurrent())return;const box=qs('#logs');if(!box)return;box.append(createLine(d));while(box.children.length>800)box.firstChild.remove();box.scrollTop=box.scrollHeight};
  const loadHistory=async()=>{const level=qs('#logLevel')?.value;if(!level)return false;try{const d=await api(`/api/logs/history?level=${encodeURIComponent(level)}&limit=800`,{signal:page.signal});if(!page.isCurrent())return false;const box=qs('#logs');if(!box)return false;const fragment=document.createDocumentFragment();(d.items||[]).forEach(item=>fragment.append(createLine(item)));box.replaceChildren(fragment);box.scrollTop=box.scrollHeight;return true}catch(e){if(isAbortError(e)||!page.isCurrent())return false;toast(e.message,true);return false}};
  const start=()=>{if(!page.isCurrent())return;if(logES)logES.close();const level=qs('#logLevel')?.value;if(!level)return;logES=new EventSource(`${PREFIX}/api/stream/logs?level=${encodeURIComponent(level)}`);logES.onmessage=e=>{try{appendLine(JSON.parse(e.data))}catch(_){}};const toggle=qs('#toggleLogs');if(toggle)toggle.textContent='停止'};
  if(!await loadHistory()||!page.isCurrent())return;start();
  qs('#logLevel').onchange=async()=>{if(logES){logES.close();logES=null}if(await loadHistory())start()};
  qs('#clearLogs').onclick=async()=>{try{await api('/api/logs/history',{method:'DELETE'});qs('#logs').innerHTML='';toast('历史日志已清空')}catch(e){toast(e.message,true)}};
  qs('#toggleLogs').onclick=()=>{if(logES){logES.close();logES=null;qs('#toggleLogs').textContent='继续'}else start()};
}

function portRow(id,label,desc,setting,locked=false){
  const enabled=locked?true:Boolean(setting?.enabled);
  const port=Number(setting?.port||0)||'';
  return `<div class="port-row" data-port-row="${id}">
    <div class="port-row-copy"><strong>${esc(label)}</strong><span>${esc(desc)}</span></div>
    <div class="port-row-control"><input id="${id}Port" class="port-input mono" type="number" min="1" max="65535" value="${esc(port)}">${locked?'<span class="port-required">必需</span>':`<label class="switch" title="${enabled?'已启用':'已关闭'}"><input id="${id}Enabled" type="checkbox" ${enabled?'checked':''}><span></span></label>`}</div>
  </div>`;
}
function optionSwitch(id,label,desc,checked,disabled=false){
  return `<label class="setting-toggle ${disabled?'disabled':''}"><div><strong>${esc(label)}</strong><span>${esc(desc)}</span></div><span class="switch"><input id="${id}" type="checkbox" ${checked?'checked':''} ${disabled?'disabled':''}><span></span></span></label>`;
}


function proxyEnvVarsMarkup(vars){
  const items=Array.isArray(vars)?vars:[];
  if(!items.length)return '<span class="proxy-env-empty">未设置代理环境变量</span>';
  return `<div class="proxy-env-vars">${items.map(v=>`<div class="proxy-env-var"><span class="mono proxy-env-key">${esc(v.key)}</span><span class="mono proxy-env-value" title="${esc(v.value)}">${esc(v.value)}</span>${v.line?`<span class="proxy-env-line">L${esc(v.line)}</span>`:''}</div>`).join('')}</div>`;
}
function proxyEnvFileMarkup(file){
  const status=!file?.exists?'不存在':file?.readable===false?'不可读':(file?.variables?.length?'已检测到代理设置':'未设置');
  const statusClass=!file?.exists||file?.readable===false?'warn':file?.variables?.length?'active':'';
  return `<div class="proxy-env-source"><div class="proxy-env-source-head"><strong class="mono">${esc(file?.path||'--')}</strong><span class="proxy-env-status ${statusClass}">${esc(status)}</span></div>${file?.error?`<div class="proxy-env-error">${esc(file.error)}</div>`:proxyEnvVarsMarkup(file?.variables)}</div>`;
}
function localProxyLoopRisk(vars,mixedPort){
  const port=Number(mixedPort||0);
  if(!port)return false;
  return (Array.isArray(vars)?vars:[]).some(v=>{
    if(/no_proxy/i.test(String(v?.key||'')))return false;
    try{const u=new URL(String(v?.value||''));return ['127.0.0.1','localhost','::1','[::1]'].includes(u.hostname)&&Number(u.port||(/https:/i.test(u.protocol)?443:80))===port}catch(_){return new RegExp(`(?:127\\.0\\.0\\.1|localhost):${port}(?:/|$)`).test(String(v?.value||''))}
  });
}

async function renderSettings(){
  const [sys,s,net,proxyEnv,appUpdate,appIcons]=await Promise.all([
    api('/api/system/status').catch(e=>({available:false,error:e.message})),
    api('/api/settings'),
    api('/api/network/settings').catch(e=>({error:e.message,settings:null,tunCapability:{supported:false}})),
    api('/api/system/proxy-environment').catch(e=>({ok:false,error:e.message,files:[],managerEnvironment:[],helperEnvironment:[],mihomoEnvironment:{pid:null,variables:[]},management:null})),
    api('/api/app/update-info').catch(e=>({appName:'Clash for fnOS',currentVersion:'--',platform:'--',sourceConfigured:false,error:e.message})),
    api('/api/app/icons').catch(e=>({ok:false,selected:'cat-orbit',defaultId:'cat-orbit',requiresWindowReload:true,error:e.message,options:[{id:'cat-orbit',name:'星环猫',description:'Clash for fnOS 默认图标',preview:'/app/clash-for-fnos/icons/cat-orbit_256.png'}]}))
  ]);
  const privOk=sys.available!==false&&sys.privileged!==false;
  const currentVersion=sys.currentVersion||sys.controllerVersion?.version||'--';
  const managed=sys.mode==='managed';
  const b=sys.bootstrap||{};
  const onlineCoreDelivery=b.delivery==='online';
  const n=net.settings||{
    controller:{port:Number(new URL(s.controller||'http://127.0.0.1:9090').port||9090),host:'127.0.0.1'},
    mixed:{enabled:true,port:Number(sys.managedMixedPort||7890)},socks:{enabled:false,port:7898},http:{enabled:false,port:7899},redir:{enabled:false,port:7895},tproxy:{enabled:false,port:7896},allowLan:false,
    tun:{enabled:false,stack:'mixed',autoRoute:true,autoRedirect:true,autoDetectInterface:true,strictRoute:false,dnsHijack:true,mtu:9000}
  };
  const tun=n.tun||{};
  const cap=net.tunCapability||{};
  const tunSupported=cap.supported!==false;
  const tunToggleAllowed=tunSupported||Boolean(tun.enabled);
  const tunSupportText=tunSupported
    ? '当前 Mihomo 具备 TUN 所需权限，可直接启用'
    : (!cap.tunDevice?'当前系统没有 /dev/net/tun，暂不能启用 TUN':'当前 Mihomo 不是 root 且没有 CAP_NET_ADMIN，暂不能启用 TUN');
  const controllerHost=String(n.controller?.host||'127.0.0.1');
  const controllerDisplayHost=!controllerHost||['0.0.0.0','::','*'].includes(controllerHost)?'所有地址（Manager 使用 127.0.0.1 连接）':controllerHost;
  const dnsWarning=Boolean(tun.enabled&&tun.dnsHijack!==false&&!n.dnsEnabled);
  const currentSettingsView=settingsView||'home';

  const currentTunPayload=(enabled=Boolean(tun.enabled))=>({
    enabled,
    stack:tun.stack||'mixed',
    mtu:Number(tun.mtu||9000),
    autoRoute:tun.autoRoute!==false,
    autoRedirect:tun.autoRedirect!==false,
    autoDetectInterface:tun.autoDetectInterface!==false,
    dnsHijack:tun.dnsHijack!==false,
    strictRoute:Boolean(tun.strictRoute)
  });
  const currentNetworkPayload=(tunPayload=currentTunPayload())=>({
    controller:{port:Number(n.controller?.port||9090)},
    mixed:{enabled:Boolean(n.mixed?.enabled),port:Number(n.mixed?.port||7890)},
    socks:{enabled:Boolean(n.socks?.enabled),port:Number(n.socks?.port||7898)},
    http:{enabled:Boolean(n.http?.enabled),port:Number(n.http?.port||7899)},
    redir:{enabled:Boolean(n.redir?.enabled),port:Number(n.redir?.port||7895)},
    tproxy:{enabled:Boolean(n.tproxy?.enabled),port:Number(n.tproxy?.port||7896)},
    allowLan:Boolean(n.allowLan),
    tun:tunPayload
  });

  const networkBlock=`<div class="settings-accordion-panel network-card">
      ${net.error?`<div class="local-warning">无法读取网络设置：${esc(net.error)}</div>`:''}
      <div class="port-list">
        ${portRow('netController','Controller API',`Mihomo REST API · 监听 ${controllerDisplayHost}`,{enabled:true,port:n.controller?.port||9090},true)}
        ${portRow('netMixed','混合代理端口','同时接受 HTTP 与 SOCKS5 代理',n.mixed)}
        ${portRow('netSocks','SOCKS5 代理端口','单独提供 SOCKS5 入站',n.socks)}
        ${portRow('netHttp','HTTP(S) 代理端口','单独提供 HTTP CONNECT/HTTP 代理',n.http)}
        ${portRow('netRedir','Redir 透明代理端口','Linux TCP REDIRECT 入站',n.redir)}
        ${portRow('netTproxy','TProxy 透明代理端口','Linux TPROXY TCP/UDP 入站',n.tproxy)}
      </div>
      <div class="network-options">${optionSwitch('netAllowLan','允许局域网连接','允许其他设备访问已启用的代理端口',Boolean(n.allowLan))}</div>
    </div>`;

  const tunBlock=`<div class="settings-accordion-panel tun-card ${tun.enabled?'tun-on':''}"><div class="section-head"><div><h2>TUN 详细设置</h2><p>接管 NAS 系统流量；首页可以快速开关，这里配置完整参数</p></div><div class="tun-master"><span class="${tunSupported?'good-text':'warn-text'}">${tun.enabled?'已开启':'已关闭'}</span><label class="switch large"><input id="tunEnabled" type="checkbox" ${tun.enabled?'checked':''} ${tunToggleAllowed?'':'disabled'}><span></span></label></div></div>
    <div class="tun-capability ${tunSupported?'ok':'warn'}"><strong>${tunSupported?'可用':'不可用'}</strong><span>${esc(tunSupportText)}</span></div>
    <div class="tun-main-grid">
      <div class="field"><label>TUN Stack</label><select id="tunStack"><option value="mixed" ${tun.stack==='mixed'?'selected':''}>mixed（推荐）</option><option value="system" ${tun.stack==='system'?'selected':''}>system</option><option value="gvisor" ${tun.stack==='gvisor'?'selected':''}>gVisor</option></select></div>
      <div class="field"><label>MTU</label><input id="tunMtu" type="number" min="1280" max="65535" value="${esc(tun.mtu||9000)}"></div>
    </div>
    <div class="tun-option-grid">
      ${optionSwitch('tunAutoRoute','自动路由','自动把系统流量路由到 TUN',tun.autoRoute!==false)}
      ${optionSwitch('tunAutoRedirect','Auto Redirect','Linux 自动配置 nftables/iptables TCP 重定向',tun.autoRedirect!==false)}
      ${optionSwitch('tunAutoDetect','自动检测出口网卡','自动选择实际的外网出口接口',tun.autoDetectInterface!==false)}
      ${optionSwitch('tunDnsHijack','DNS 劫持','劫持 UDP/TCP 53 到 Mihomo DNS 模块',tun.dnsHijack!==false)}
      ${optionSwitch('tunStrictRoute','严格路由','减少流量/DNS 泄漏；复杂网络环境可能影响其他虚拟网卡',Boolean(tun.strictRoute))}
    </div>
    ${dnsWarning?'<div class="tun-capability warn"><strong>DNS</strong><span>当前配置未检测到 <span class="mono">dns.enable: true</span>，开启 DNS 劫持前建议先在配置中启用 Mihomo DNS。</span></div>':''}
    <div class="tun-note"><strong>注意</strong><span>TUN 会修改 fnOS 的系统路由与 DNS 流向。默认关闭；如果当前订阅/配置本身不可用，开启 TUN 可能影响 NAS 访问互联网。</span></div>
  </div>`;

  const appIconOptions=Array.isArray(appIcons?.options)&&appIcons.options.length?appIcons.options:[{id:'cat-orbit',name:'星环猫',description:'Clash for fnOS 默认图标',preview:'/app/clash-for-fnos/icons/cat-orbit_256.png'}];
  const selectedAppIcon=String(appIcons?.selected||appIcons?.defaultId||'cat-orbit');
  scheduleFnosWindowBrand(selectedAppIcon);
  const appIconBlock=`<div class="app-icon-settings"><div class="section-head"><div><h2>软件图标</h2></div></div>
      ${appIcons?.error?`<div class="local-warning">读取图标设置失败：${esc(appIcons.error)}</div>`:''}
      <div class="app-icon-picker">${appIconOptions.map(icon=>{const active=icon.id===selectedAppIcon;return `<button type="button" class="app-icon-choice ${active?'active':''}" data-app-icon="${esc(icon.id)}" title="${esc(icon.name||icon.id)}" aria-label="${esc(icon.name||icon.id)}" ${appIcons?.ok===false?'disabled':''}><img src="${esc(icon.preview||`/app/clash-for-fnos/icons/${icon.id}_256.png`)}" alt=""><span class="app-icon-choice-state">${active?'✓':''}</span></button>`}).join('')}</div>
    </div>`;

  const behaviorBlock=`<div class="settings-accordion-panel">${appIconBlock}<div class="settings-inner-divider"></div><div class="section-head"><div><h2>内核行为</h2><p>Controller 与 Secret 从当前 Mihomo 启动配置自动检测，不需要手工维护 URL</p></div><span class="auto-detected">自动管理</span></div>
      <div class="system-grid"><div><span class="system-label">Controller</span><strong class="mono">${esc(s.controller)}</strong></div><div><span class="system-label">Secret</span><span>${s.hasSecret?'已从配置读取':'配置中未设置'}</span></div></div>
      <div class="form-grid" style="margin-top:14px"><div class="field"><label>延迟测试 URL</label><input id="healthUrl" value="${esc(s.healthcheckUrl)}"></div><div class="field"><label>超时（毫秒）</label><input id="healthTimeout" type="number" value="${esc(s.healthcheckTimeout)}"></div><div class="field full"><label><input id="persistSel" type="checkbox" style="width:auto" ${s.persistSelections?'checked':''}> 记住策略组选择</label></div><div class="field full"><label><input id="applyOnStart" type="checkbox" style="width:auto" ${s.applyManagedConfigOnStart?'checked':''}> Manager 启动后重新应用已保存配置</label></div></div>
      <div class="actions settings-actions"><button class="ghost" id="testSettings">测试 Controller</button></div>
    </div>`;

  const proxyMgmt=proxyEnv?.management||null;
  const proxyMgmtSettings=proxyMgmt?.settings||{enabled:true,followMixedPort:true,port:Number(n.mixed?.port||7890),noProxy:'localhost,127.0.0.1,::1',targets:{environment:true,profile:true,bashrc:true}};
  const proxyMgmtTargets=proxyMgmtSettings.targets||{environment:true,profile:true,bashrc:true};
  const proxyMgmtAvailable=Boolean(privOk&&proxyMgmt);
  const proxyMgmtActive=proxyMgmt?.active===true;
  const proxyMgmtWaiting=proxyMgmtSettings.enabled===true&&!proxyMgmtActive&&proxyMgmt?.suspendedReason==='mixed-port-disabled';
  const proxyMgmtStatus=!proxyMgmtSettings.enabled?'已关闭':proxyMgmtActive?'已启用':proxyMgmtWaiting?'等待 Mixed Port':'已配置';
  const proxyMgmtPort=Number(proxyMgmtSettings.followMixedPort?(n.mixed?.port||proxyMgmt?.mixedPort||7890):(proxyMgmtSettings.port||7890));
  const mihomoProxyVars=proxyEnv?.mihomoEnvironment?.variables||[];
  const proxyLoopRisk=localProxyLoopRisk(mihomoProxyVars,n.mixed?.port);
  const systemProxyBlock=`<div class="settings-accordion-panel proxy-env-card">
      ${proxyEnv?.error?`<div class="local-warning">读取系统代理环境失败：${esc(proxyEnv.error)}</div>`:''}
      <div class="proxy-env-manage-card">
        <div class="proxy-env-manage-head"><div><h3>代理环境变量</h3><p>为支持 HTTP_PROXY / HTTPS_PROXY 的命令行与登录会话提供本机 Mihomo 代理</p></div><span id="proxyEnvManagedStatus" class="proxy-env-status ${proxyMgmtActive?'active':proxyMgmtWaiting?'warn':''}">${esc(proxyMgmtStatus)}</span></div>
        ${!proxyMgmtAvailable?`<div class="local-warning">Root Helper 不可用，当前只能查看环境变量，无法修改系统文件。</div>`:''}
        <div class="proxy-env-manage-grid">
          ${optionSwitch('proxyEnvEnabled','启用代理环境变量','关闭时仅移除 Clash for fnos 自己管理的配置块',Boolean(proxyMgmtSettings.enabled),!proxyMgmtAvailable)}
          ${optionSwitch('proxyEnvFollowMixed','自动跟随 Mixed Port','混合代理端口变化时自动同步；关闭 Mixed Port 时自动暂停写入',proxyMgmtSettings.followMixedPort!==false,!proxyMgmtAvailable)}
          <div class="field proxy-env-port-field"><label>代理地址</label><div class="proxy-env-address"><span class="mono">127.0.0.1 :</span><input id="proxyEnvPort" class="mono" type="number" min="1" max="65535" value="${esc(proxyMgmtPort)}" ${proxyMgmtSettings.followMixedPort!==false||!proxyMgmtAvailable?'disabled':''}></div><small>${proxyMgmtSettings.followMixedPort!==false?`跟随当前 Mixed Port · ${n.mixed?.enabled?'正在监听':'当前未启用'}`:'使用手动端口'}</small></div>
          <div class="field proxy-env-no-proxy"><label>NO_PROXY</label><input id="proxyEnvNoProxy" class="mono" value="${esc(proxyMgmtSettings.noProxy||'localhost,127.0.0.1,::1')}" ${proxyMgmtAvailable?'':'disabled'}><small>逗号分隔；默认排除 localhost / 127.0.0.1 / ::1</small></div>
        </div>
        <div class="proxy-env-target-title">应用范围</div>
        <div class="proxy-env-target-grid">
          ${optionSwitch('proxyEnvTargetEnvironment','系统登录环境','/etc/environment · 默认开启',proxyMgmtTargets.environment!==false,!proxyMgmtAvailable)}
          ${optionSwitch('proxyEnvTargetProfile','登录 Shell','/etc/profile · 默认开启',proxyMgmtTargets.profile!==false,!proxyMgmtAvailable)}
          ${optionSwitch('proxyEnvTargetBashrc','Bash 交互环境','/etc/bash.bashrc · 默认开启',proxyMgmtTargets.bashrc!==false,!proxyMgmtAvailable)}
        </div>
        <div class="hint proxy-env-manage-note">修改前会自动备份原文件；关闭后只移除 <span class="mono">Clash for fnos proxy</span> 管理块，不覆盖用户其他内容。<span class="mono">/etc/environment</span> 不写 <span class="mono">export</span>，Shell 文件使用 <span class="mono">export</span>。新值主要对新登录会话生效。托管 Mihomo 启动时会主动移除自身 HTTP(S)/ALL_PROXY 继承，避免代理回环。</div>
      </div>
      ${proxyLoopRisk?`<div class="proxy-env-loop-warning"><strong>检测到可能的自身代理回环</strong><span>当前 Mihomo 进程继承的代理变量指向混合代理端口 <span class="mono">127.0.0.1:${esc(n.mixed?.port)}</span>。外部 Core 请重启并清理其服务环境；Manager 托管 Core 会自动剥离这些变量。</span></div>`:''}
      <div class="proxy-env-divider"><span>当前检测结果</span></div>
      <div class="proxy-env-files">${(proxyEnv?.files||[]).map(proxyEnvFileMarkup).join('')||'<div class="proxy-env-empty">未读取到系统环境文件</div>'}</div>
      <div class="proxy-env-runtime">
        <div class="proxy-env-runtime-item"><div class="proxy-env-source-head"><strong>Clash for fnos Web 进程</strong><span class="proxy-env-status ${proxyEnv?.managerEnvironment?.length?'active':''}">${proxyEnv?.managerEnvironment?.length?'已继承':'未继承'}</span></div>${proxyEnvVarsMarkup(proxyEnv?.managerEnvironment)}</div>
        <div class="proxy-env-runtime-item"><div class="proxy-env-source-head"><strong>Root Helper 进程</strong><span class="proxy-env-status ${proxyEnv?.helperEnvironment?.length?'active':''}">${proxyEnv?.helperEnvironment?.length?'已继承':'未继承'}</span></div>${proxyEnvVarsMarkup(proxyEnv?.helperEnvironment)}</div>
        <div class="proxy-env-runtime-item"><div class="proxy-env-source-head"><strong>Mihomo 进程${proxyEnv?.mihomoEnvironment?.pid?` · PID ${esc(proxyEnv.mihomoEnvironment.pid)}`:''}</strong><span class="proxy-env-status ${mihomoProxyVars.length?'active':''}">${mihomoProxyVars.length?'已继承':'未继承'}</span></div>${proxyEnvVarsMarkup(mihomoProxyVars)}</div>
      </div>
      <div class="hint" style="margin-top:12px"><span class="mono">/etc/profile</span> 和 <span class="mono">/etc/bash.bashrc</span> 主要作用于 Shell；fnOS 后台服务、Docker 容器以及不读取代理环境变量的程序不一定受影响。需要透明接管系统 TCP/UDP 流量时仍应使用 TUN。</div>
    </div>`;

  const appUpdateBlock=`<div class="settings-accordion-subsection system-card app-update-card"><div class="section-head"><div><h2>Clash for fnOS 更新</h2><p>应用自身版本检测与升级渠道</p></div><div class="actions"><button class="ghost" id="checkAppUpdate">检查更新</button></div></div>
    <div class="system-grid"><div><span class="system-label">当前版本</span><strong>v${esc(String(appUpdate?.currentVersion||'--').replace(/^v/,''))}</strong></div><div><span class="system-label">平台</span><strong>${esc(appUpdate?.platform==='arm'?'ARM':'x86')}</strong></div><div><span class="system-label">更新渠道</span><span class="app-update-channel">${appUpdate?.sourceConfigured?'<span class="good-text">GitHub Releases</span>':'fnOS 应用中心 / 手动 FPK'}</span></div><div><span class="system-label">发布源</span><span class="mono">${esc(appUpdate?.releaseRepo||'未绑定公开发布源')}</span></div></div>
    <div class="hint app-update-source-note">${appUpdate?.sourceConfigured?'点击“检查更新”会通过当前网络策略访问已绑定的 GitHub Releases，并匹配当前 CPU 架构的 FPK。应用本体升级仍交由 fnOS 应用中心/手动安装 FPK 完成，避免运行中的应用自替换导致升级中断。':'当前构建尚未绑定公开 Release 仓库，因此无法在线判断是否有新版本。现阶段请通过 fnOS 应用中心或手动安装新版 FPK 升级；发布仓库确定后即可启用在线版本检查。'}</div>
  </div>`;

  const coreBlock=`<div class="settings-accordion-subsection system-card"><div class="section-head"><div><h2>Mihomo Core</h2><p>${managed?(onlineCoreDelivery?'由 Manager 按运行平台从官方 Release 获取 Core':'由 Manager 使用安装包内置 Core 启动，并支持在线更新'):'管理当前外部 Mihomo Core'}</p></div><div class="actions">${b.state==='error'?'<button id="retryBootstrap">重新检测并启用</button>':''}<button class="ghost" id="checkCoreUpdate">检查更新</button></div></div>
    ${privOk?`<div class="system-grid"><div><span class="system-label">模式</span><strong>${managed?'Manager 托管':'外部 Core'}</strong></div><div><span class="system-label">当前版本</span><strong>${esc(currentVersion)}</strong></div><div><span class="system-label">二进制</span><span class="mono">${esc(sys.binaryPath||'--')}</span></div><div><span class="system-label">启动配置</span><span class="mono">${esc(sys.configPath||'--')}</span></div></div>`:`<div class="local-warning">特权 helper 不可用：${esc(sys.error||'无法执行系统级配置同步和内核更新')}</div>`}
    <div class="hint" style="margin-top:12px">${managed?(onlineCoreDelivery?'当前 all 通用 FPK 不包含 Core。全新 fnOS 未检测到 Mihomo 时，Manager 会识别 CPU 架构，从 MetaCubeX/mihomo GitHub Release 下载匹配资产，校验 SHA-256 后自动启动；首次启用需要访问 GitHub。':'全新 fnOS 未检测到 Mihomo 时，Manager 会优先使用当前 FPK 内置、与平台匹配的官方 Mihomo Core，校验 SHA-256 后自动启动；首次启用无需访问 GitHub。后续可在这里显式检查并在线更新 Core。'):'外部 Core 不会被 Manager 自动替换或启动；在线更新前会备份原二进制。'}</div>
  </div>`;

  const tunSettingsBlock=tunBlock;
  const updateSettingsBlock=`${coreBlock}${appUpdateBlock}`;

  const accordionItem=(key,icon,title,desc,content)=>{
    const open=currentSettingsView===key;
    return `<div class="settings-accordion-item ${open?'open':''}">
      <button class="settings-accordion-head" data-settings-accordion="${key}" type="button" aria-expanded="${open?'true':'false'}">
        <span class="settings-category-icon">${icon}</span>
        <span class="settings-category-copy"><strong>${title}</strong><small>${desc}</small></span>
        <span class="settings-accordion-chevron">⌄</span>
      </button>
      ${open?`<div class="settings-accordion-body">${content}</div>`:''}
    </div>`;
  };

  const homeBlock=`<div class="settings-home">
    <div class="settings-accordion-list">
      ${accordionItem('network','◎','网络与端口','SOCKS5、HTTP、Redir、TProxy 与局域网选项',networkBlock)}
      ${accordionItem('tun','◇','TUN 设置','TUN 详细参数与系统流量接管',tunSettingsBlock)}
      ${accordionItem('advanced','⌘','环境变量设置','管理系统登录与 Shell 的代理环境变量',systemProxyBlock)}
      ${accordionItem('behavior','⚙','其他设置','内核参数、策略选择与启动行为',behaviorBlock)}
      ${accordionItem('update','↻','更新设置','Clash for fnOS 与 Mihomo Core 版本检测与更新',updateSettingsBlock)}
    </div>
  </div>`;

  qs('#pageDesc').textContent='修改后自动保存并立即应用';
  qs('#content').innerHTML=homeBlock;

  document.querySelectorAll('[data-settings-accordion]').forEach(btn=>btn.onclick=()=>{
    const key=btn.dataset.settingsAccordion;
    settingsView=currentSettingsView===key?'home':key;
    renderSettings();
  });

  if(currentSettingsView==='network'){
    const portPairs=[['netMixedEnabled','netMixedPort'],['netSocksEnabled','netSocksPort'],['netHttpEnabled','netHttpPort'],['netRedirEnabled','netRedirPort'],['netTproxyEnabled','netTproxyPort']];
    const payload=()=>({
      controller:{port:Number(qs('#netControllerPort').value)},
      mixed:{enabled:qs('#netMixedEnabled').checked,port:Number(qs('#netMixedPort').value)},
      socks:{enabled:!qs('#netMixedEnabled').checked&&qs('#netSocksEnabled').checked,port:Number(qs('#netSocksPort').value)},
      http:{enabled:!qs('#netMixedEnabled').checked&&qs('#netHttpEnabled').checked,port:Number(qs('#netHttpPort').value)},
      redir:{enabled:!qs('#netMixedEnabled').checked&&qs('#netRedirEnabled').checked,port:Number(qs('#netRedirPort').value)},
      tproxy:{enabled:!qs('#netMixedEnabled').checked&&qs('#netTproxyEnabled').checked,port:Number(qs('#netTproxyPort').value)},
      allowLan:qs('#netAllowLan').checked,
      tun:currentTunPayload()
    });
    const autoSaveNetwork=(delay=250)=>queueSettingsAutoSave('network',payload(),delay);
    const syncPortFields=()=>{
      const mixedOn=qs('#netMixedEnabled')?.checked===true;
      portPairs.forEach(([toggle,input],idx)=>{
        const toggleEl=qs(`#${toggle}`),inputEl=qs(`#${input}`);if(!toggleEl||!inputEl)return;
        const lockedByMixed=mixedOn&&idx>0;toggleEl.disabled=lockedByMixed;inputEl.disabled=lockedByMixed||!toggleEl.checked;
        const row=document.querySelector(`[data-port-row="${toggle.replace('Enabled','')}"]`);if(row){row.classList.toggle('mixed-disabled',lockedByMixed);row.setAttribute('aria-disabled',lockedByMixed?'true':'false');row.title=lockedByMixed?'混合代理已启用；关闭混合代理后可单独配置此入口':'';}
      });
    };
    portPairs.forEach(([toggle,input])=>{
      const toggleEl=qs(`#${toggle}`),inputEl=qs(`#${input}`);
      if(toggleEl)toggleEl.onchange=()=>{syncPortFields();autoSaveNetwork(120)};
      if(inputEl)inputEl.onchange=()=>autoSaveNetwork(250);
    });
    if(qs('#netControllerPort'))qs('#netControllerPort').onchange=()=>autoSaveNetwork(250);
    if(qs('#netAllowLan'))qs('#netAllowLan').onchange=()=>autoSaveNetwork(120);
    syncPortFields();
  }

  if(currentSettingsView==='tun'){
    const tunPayload=()=>({enabled:qs('#tunEnabled').checked,stack:qs('#tunStack').value,mtu:Number(qs('#tunMtu').value),autoRoute:qs('#tunAutoRoute').checked,autoRedirect:qs('#tunAutoRedirect').checked,autoDetectInterface:qs('#tunAutoDetect').checked,dnsHijack:qs('#tunDnsHijack').checked,strictRoute:qs('#tunStrictRoute').checked});
    const autoSaveTun=(delay=250)=>queueSettingsAutoSave('network',currentNetworkPayload(tunPayload()),delay);
    const syncTunFields=()=>{const enabled=qs('#tunEnabled')?.checked===true;['#tunStack','#tunMtu','#tunAutoRoute','#tunAutoDetect','#tunDnsHijack','#tunStrictRoute'].forEach(id=>{if(qs(id))qs(id).disabled=!enabled||!tunSupported});if(qs('#tunAutoRedirect')){const routeOn=qs('#tunAutoRoute')?.checked!==false;if(!routeOn)qs('#tunAutoRedirect').checked=false;qs('#tunAutoRedirect').disabled=!enabled||!tunSupported||!routeOn;}};
    if(qs('#tunEnabled'))qs('#tunEnabled').onchange=()=>{syncTunFields();qs('.tun-card')?.classList.toggle('tun-on',qs('#tunEnabled').checked);autoSaveTun(120)};
    if(qs('#tunAutoRoute'))qs('#tunAutoRoute').onchange=()=>{syncTunFields();autoSaveTun(120)};
    ['tunAutoRedirect','tunAutoDetect','tunDnsHijack','tunStrictRoute'].forEach(id=>{if(qs(`#${id}`))qs(`#${id}`).onchange=()=>autoSaveTun(120)});
    if(qs('#tunStack'))qs('#tunStack').onchange=()=>autoSaveTun(150);
    if(qs('#tunMtu'))qs('#tunMtu').onchange=()=>autoSaveTun(250);
    syncTunFields();
  }

  if(currentSettingsView==='behavior'){
    document.querySelectorAll('[data-app-icon]').forEach(btn=>btn.onclick=async()=>{const id=btn.dataset.appIcon;if(!id||id===selectedAppIcon)return;busy(btn);try{const r=await api('/api/app/icon',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({iconId:id})});scheduleFnosWindowBrand(r?.selected||id);const reg=r?.desktopRegistration;if(reg?.matched){toast('软件图标已切换；刷新 fnOS 桌面并重新打开窗口后生效')}else{toast(reg?.warning?`图标已保存，但桌面入口同步未完成：${reg.warning}`:'软件图标已保存；刷新 fnOS 桌面并重新打开窗口后生效',Boolean(reg?.warning))}renderSettings()}catch(e){toast(e.message,true)}finally{busy(btn,false)}});
    const behaviorPayload=()=>({controllerAutoDetect:true,persistSelections:qs('#persistSel').checked,applyManagedConfigOnStart:qs('#applyOnStart').checked,healthcheckUrl:qs('#healthUrl').value,healthcheckTimeout:Number(qs('#healthTimeout').value)});
    const autoSaveBehavior=(delay=250)=>queueSettingsAutoSave('behavior',behaviorPayload(),delay);
    ['persistSel','applyOnStart'].forEach(id=>{if(qs(`#${id}`))qs(`#${id}`).onchange=()=>autoSaveBehavior(120)});
    if(qs('#healthUrl'))qs('#healthUrl').onchange=()=>autoSaveBehavior(250);
    if(qs('#healthTimeout'))qs('#healthTimeout').onchange=()=>autoSaveBehavior(250);
    qs('#testSettings').onclick=async()=>{const btn=qs('#testSettings');busy(btn);try{const currentPayload=behaviorPayload();await api('/api/settings',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(currentPayload)});settingsAutoSaveState.behavior.lastJson=JSON.stringify(currentPayload);const d=await api('/api/settings/test',{method:'POST'});toast(`连接成功：${d.version?.version||'Mihomo'}`);coreHealth().catch(()=>{})}catch(e){toast(e.message,true)}finally{busy(btn,false)}};
  }

  if(currentSettingsView==='advanced'){
    const proxyEnvPayload=()=>({
      enabled:qs('#proxyEnvEnabled')?.checked===true,
      followMixedPort:qs('#proxyEnvFollowMixed')?.checked!==false,
      port:Number(qs('#proxyEnvPort')?.value||proxyMgmtPort||7890),
      noProxy:String(qs('#proxyEnvNoProxy')?.value||'localhost,127.0.0.1,::1').trim(),
      targets:{
        environment:qs('#proxyEnvTargetEnvironment')?.checked===true,
        profile:qs('#proxyEnvTargetProfile')?.checked===true,
        bashrc:qs('#proxyEnvTargetBashrc')?.checked===true
      }
    });
    const syncProxyEnvFields=()=>{
      const follow=qs('#proxyEnvFollowMixed')?.checked!==false;
      if(qs('#proxyEnvPort')){qs('#proxyEnvPort').disabled=!proxyMgmtAvailable||follow;if(follow)qs('#proxyEnvPort').value=String(n.mixed?.port||proxyMgmtPort||7890)}
    };
    const saveProxyEnv=(delay=260)=>queueProxyEnvironmentAutoSave(proxyEnvPayload(),delay);
    ['proxyEnvEnabled','proxyEnvTargetEnvironment','proxyEnvTargetProfile','proxyEnvTargetBashrc'].forEach(id=>{if(qs(`#${id}`))qs(`#${id}`).onchange=()=>saveProxyEnv(120)});
    if(qs('#proxyEnvFollowMixed'))qs('#proxyEnvFollowMixed').onchange=()=>{syncProxyEnvFields();saveProxyEnv(120)};
    if(qs('#proxyEnvPort'))qs('#proxyEnvPort').onchange=()=>saveProxyEnv(240);
    if(qs('#proxyEnvNoProxy'))qs('#proxyEnvNoProxy').onchange=()=>saveProxyEnv(240);
    syncProxyEnvFields();
  }

  if(currentSettingsView==='update'){
    if(qs('#checkAppUpdate'))qs('#checkAppUpdate').onclick=async()=>{const btn=qs('#checkAppUpdate');busy(btn);try{const d=await api('/api/app/check-update',{method:'POST'});if(!d.sourceConfigured){modal(`<h3>Clash for fnOS 更新</h3><div class="system-update-summary"><div><span>当前版本</span><strong>v${esc(String(d.currentVersion||'--').replace(/^v/,''))}</strong></div><div><span>平台</span><strong>${esc(d.platform==='arm'?'ARM':'x86')}</strong></div><div><span>更新渠道</span><strong>fnOS / FPK</strong></div></div><div class="hint" style="margin:12px 0">当前构建没有绑定公开 Release 仓库，暂时无法在线判断新版本。请通过 fnOS 应用中心或手动安装新版 FPK 完成应用升级。</div><div class="actions"><button class="ghost" data-modal-close>关闭</button></div>`);return}const latest=d.latest||{};const update=d.updateAvailable===true;modal(`<h3>Clash for fnOS 更新</h3><div class="system-update-summary"><div><span>当前</span><strong>v${esc(String(d.currentVersion||'--').replace(/^v/,''))}</strong></div><div><span>最新</span><strong>${esc(latest.tag||'--')}</strong></div><div><span>平台</span><strong>${esc(d.platform==='arm'?'ARM':'x86')}</strong></div></div>${latest.asset?`<div class="app-update-latest"><strong>${esc(latest.asset.name||'FPK')}</strong><div class="tiny" style="margin-top:5px">${latest.publishedAt?`发布时间：${esc(new Date(latest.publishedAt).toLocaleString())}`:''}</div></div>`:'<div class="local-warning" style="margin-top:12px">该 Release 未找到与当前架构匹配的 FPK，请到发布页确认。</div>'}<div class="actions" style="margin-top:12px">${latest.htmlUrl?`<button class="ghost" id="openAppRelease">打开发布页</button>`:''}${update&&latest.asset?.url?`<button id="downloadAppFpk">下载 FPK</button>`:''}<button class="ghost" data-modal-close>关闭</button></div>${update?'':'<div class="good-text" style="margin-top:12px">当前已经是最新版本。</div>'}`);if(qs('#openAppRelease'))qs('#openAppRelease').onclick=()=>window.open(latest.htmlUrl,'_blank','noopener');if(qs('#downloadAppFpk'))qs('#downloadAppFpk').onclick=()=>window.open(latest.asset.url,'_blank','noopener')}catch(e){toast(e.message,true)}finally{busy(btn,false)}};
    if(qs('#retryBootstrap'))qs('#retryBootstrap').onclick=async()=>{const btn=qs('#retryBootstrap');busy(btn);try{await api('/api/core/bootstrap/retry',{method:'POST'});toast('Mihomo Core 已准备完成');renderSettings();coreHealth().catch(()=>{})}catch(e){toast(e.message,true);renderSettings()}finally{busy(btn,false)}};
    if(qs('#checkCoreUpdate'))qs('#checkCoreUpdate').onclick=async()=>{const btn=qs('#checkCoreUpdate');busy(btn);try{const d=await api('/api/core/check-update',{method:'POST'});const latest=d.latest;const update=d.updateAvailable===true;modal(`<h3>Mihomo Core 更新</h3><div class="system-update-summary"><div><span>当前</span><strong>${esc(d.currentVersion||'--')}</strong></div><div><span>官方最新</span><strong>${esc(latest?.tag||'--')}</strong></div><div><span>模式</span><strong>${d.mode==='managed'?'Manager 托管':'外部 Core'}</strong></div></div><div class="hint" style="margin:12px 0">资产：<span class="mono">${esc(latest?.asset?.name||'--')}</span><br>SHA-256：<span class="mono tiny">${esc(latest?.asset?.sha256||'官方未提供')}</span></div>${update?`<div class="actions"><button id="coreUpdateFile">仅更新内核文件</button>${d.canRestartService?`<button class="success" id="coreUpdateRestart">${d.mode==='managed'?'更新并重启 Core':'更新并重启服务'}</button>`:''}<button class="ghost" data-modal-close>取消</button></div>`:`<div class="good-text">当前已经是最新版本。</div><div class="actions" style="margin-top:12px"><button class="ghost" data-modal-close>关闭</button></div>`}`);if(update){qs('#coreUpdateFile').onclick=()=>runCoreUpdate(false,qs('#coreUpdateFile'));if(qs('#coreUpdateRestart'))qs('#coreUpdateRestart').onclick=()=>runCoreUpdate(true,qs('#coreUpdateRestart'))}}catch(e){toast(e.message,true)}finally{busy(btn,false)}};
  }
}
async function runCoreUpdate(restart,btn){
  const text=restart?'将从官方 GitHub 下载并校验新内核，备份当前二进制后替换，并重启当前可安全管理的 Mihomo Core。若新版本无法上线会尝试回滚。继续？':'将从官方 GitHub 下载并校验新内核，备份当前二进制后替换。当前运行进程不会立即重启，下次重启 Core/Manager 后使用新版本。继续？';
  if(!confirm(text))return;busy(btn);try{const r=await api('/api/core/update',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({restart})});if(r.alreadyLatest){toast('当前已经是最新 Mihomo');closeModal();renderSettings();return}closeModal();toast(restart?`Mihomo 已更新到 ${r.release?.tag||'新版本'}`:`内核文件已更新到 ${r.release?.tag||'新版本'}，重启 Core 后生效`);renderSettings();coreHealth().catch(()=>{})}catch(e){toast(e.message,true)}finally{busy(btn,false)}}

api('/api/app/icons').then(r=>scheduleFnosWindowBrand(r?.selected||r?.defaultId||'cat-orbit')).catch(()=>scheduleFnosWindowBrand('cat-orbit'));
buildNav();setPage(location.hash.slice(1)||'dashboard');
setInterval(()=>coreHealth().catch(()=>{}),30000);
