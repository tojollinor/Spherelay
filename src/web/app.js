'use strict';
const root=document.getElementById('root');
let model={page:'dashboard',state:null,info:null,cameraId:'',viewId:'',discover:[],discoveryReport:null,discoveryBusy:false,discoveryNetworks:null,discoveryNetwork:'',discoveryPorts:'80,443',discoveryCredentials:null,discoveryCredentialId:'',discoveryUseCredentials:false,logs:[],overlayDrag:false,changed:false};
const colors=['#65c7ea','#f8c261','#85d8a6','#b49aef','#f39094','#e1cf7e','#70b2fa','#f1a1e7'];
const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const color=i=>colors[i%colors.length];
const camList=()=>model.state?.cameras||[];
const views=()=>model.state?.streams||[];
const selectedCam=()=>camList().find(c=>c.id===model.cameraId);
const camViews=()=>views().filter(v=>v.cameraId===model.cameraId);
const selectedView=()=>camViews().find(v=>v.id===model.viewId);
const pending=new Set();
async function api(path,method='GET',body){const opt={method,credentials:'same-origin',headers:{}};if(method!=='GET'){opt.headers['Content-Type']='application/json';opt.headers['X-Spherelay-Request']='1';opt.body=JSON.stringify(body??{});}const res=await fetch('/api/'+path,opt);let data={};try{data=await res.json();}catch{}if(!res.ok)throw Error(data.error||`HTTP ${res.status}`);return data;}
function notify(message,error=false){const box=document.createElement('div');box.className='messages'+(error?' error':'');box.textContent=message;document.body.appendChild(box);setTimeout(()=>box.remove(),5300)}
let stateLoadGeneration=0;
function restoreRoute(){const [page,query='']=location.hash.slice(1).split('?');model.page=['dashboard','cameras','editor','streams','system'].includes(page)?page:'dashboard';const q=new URLSearchParams(query);model.cameraId=q.get('camera')||'';model.viewId=q.get('view')||'';if(model.page==='editor')model.editorTab=q.get('tab')||'alignment';if(model.page==='system')model.systemTab=q.get('tab')||'components'}
function rememberRoute(push=false){const q=new URLSearchParams();if(model.page==='editor'){if(model.cameraId)q.set('camera',model.cameraId);if(model.viewId)q.set('view',model.viewId);if(model.editorTab)q.set('tab',model.editorTab)}if(model.page==='system'&&model.systemTab)q.set('tab',model.systemTab);const hash='#'+model.page+(q.size?'?'+q:'');if(location.hash!==hash)history[push?'pushState':'replaceState'](null,'',hash)}
async function navigate(page){if(page==='editor'&&model.page!=='editor')invalidateEditorThumbnails();model.page=page;model.changed=false;stopEditorPreview();rememberRoute(true);await loadState()}
async function loadState(){const generation=++stateLoadGeneration;try{const state=await api('state');if(generation!==stateLoadGeneration)return;model.state=state;
 if(model.page==='cameras'||model.discoveryNetworks===null){try{model.discoveryNetworks=(await api('discovery/networks')).networks||[]}catch{model.discoveryNetworks=[]}}
 if(model.page==='cameras'||model.discoveryCredentials===null){try{model.discoveryCredentials=await api('discovery/credentials');if(!model.discoveryCredentials.profiles?.some(p=>p.id===model.discoveryCredentialId))model.discoveryCredentialId=model.discoveryCredentials.profiles?.[0]?.id||'';model.discoveryUseCredentials=!!model.discoveryCredentials.hasPassword}catch{model.discoveryCredentials={hasPassword:false,username:''}}}
 if(model.page==='system'){try{model.logs=(await api('logs')).lines||[]}catch{}}
 if(generation!==stateLoadGeneration)return;if(!camList().some(c=>c.id===model.cameraId))model.cameraId=camList()[0]?.id||'';if(!camViews().some(s=>s.id===model.viewId))model.viewId=camViews()[0]?.id||'';model.changed=false;render();if(model.savedRestartCamera){const id=model.savedRestartCamera;model.savedRestartCamera='';promptCameraRestart(id)}return true
 }catch(e){if(generation!==stateLoadGeneration)return;if(e.message.includes('Anmeldung'))showAuth(false);else notify(e.message,true);return false}}
window.addEventListener('hashchange',async()=>{
  if(model.page==='editor'&&model.changed){
    const destination=location.hash;
    rememberRoute();
    if(!await confirmEditorLeave())return;
    history.replaceState(null,'',destination||location.pathname+location.search);
  }
  restoreRoute();
  await loadState();
});
window.addEventListener('beforeunload',event=>{
  if(model.page==='editor'&&model.changed){event.preventDefault();event.returnValue='';}
});
async function init(){restoreRoute();try{model.info=await api('info');if(!model.info.setup)showAuth(true);else await loadState()}catch(e){root.innerHTML=`<div class="auth"><div class="auth-card"><h1>Verbindung fehlgeschlagen</h1><p>${esc(e.message)}</p><button class="btn" data-action="reload">Erneut versuchen</button></div></div>`}}
function showAuth(first){stopStreamPolling();root.innerHTML=`<div class="auth"><form id="login" class="auth-card"><div class="brand"><img src="/logo.svg" alt=""><div><strong>Spherelay</strong><small>One camera. Every angle.</small></div></div><div class="eyebrow">${first?'Sichere Ersteinrichtung':'Anmeldung'}</div><h1>${first?'Administratorzugang anlegen':'Willkommen zurück'}</h1><p>${first?'Die Ersteinrichtung ist ausschließlich am Windows-PC selbst möglich. Ein Passwort mit mindestens 12 Zeichen festlegen.':'Melde dich an, um Kameras und Streams zu verwalten.'}</p><div class="field"><label>Administrator-Passwort</label><input type="password" id="password" minlength="${first?'12':'1'}" autocomplete="${first?'new-password':'current-password'}" required autofocus></div>${first?'<div class="field"><label>Passwort wiederholen</label><input type="password" id="password2" minlength="12" required></div>':''}<button class="btn primary" type="submit" style="width:100%">${first?'Einrichtung abschließen':'Anmelden'}</button><p class="minor">Die Zugangsdaten bleiben lokal auf diesem Computer.</p></form></div>`;document.getElementById('login').onsubmit=async e=>{e.preventDefault();let password=document.getElementById('password').value;try{if(first){if(password!==document.getElementById('password2').value)throw Error('Passwörter stimmen nicht überein');await api('setup','POST',{password});notify('Administrator eingerichtet. Bitte anmelden.');showAuth(false)}else{await api('login','POST',{password});await loadState()}}catch(err){notify(err.message,true)}}}
function icon(name){const shapes={dashboard:'<rect x="3" y="3" width="7" height="7" rx="1.4"/><rect x="14" y="3" width="7" height="7" rx="1.4"/><rect x="3" y="14" width="7" height="7" rx="1.4"/><rect x="14" y="14" width="7" height="7" rx="1.4"/>',cameras:'<rect x="3" y="6" width="13" height="12" rx="2"/><path d="m16 10 5-3v10l-5-3"/><circle cx="9.5" cy="12" r="2.5"/>',editor:'<path d="M3 6h7m4 0h7M3 12h2m4 0h12M3 18h10m4 0h4"/><circle cx="12" cy="6" r="2"/><circle cx="7" cy="12" r="2"/><circle cx="15" cy="18" r="2"/>',streams:'<circle cx="5" cy="12" r="2"/><circle cx="19" cy="5" r="2"/><circle cx="19" cy="12" r="2"/><circle cx="19" cy="19" r="2"/><path d="M7 12h10M9 12V5h8M9 12v7h8"/>',system:'<path d="M19.483 8.900 L21.841 9.728 L21.841 14.272 L19.483 15.100 L19.483 15.100 L20.565 17.352 L17.352 20.565 L15.100 19.483 L15.100 19.483 L14.272 21.841 L9.728 21.841 L8.900 19.483 L8.900 19.483 L6.648 20.565 L3.435 17.352 L4.517 15.100 L4.517 15.100 L2.159 14.272 L2.159 9.728 L4.517 8.900 L4.517 8.900 L3.435 6.648 L6.648 3.435 L8.900 4.517 L8.900 4.517 L9.728 2.159 L14.272 2.159 L15.100 4.517 L15.100 4.517 L17.352 3.435 L20.565 6.648 L19.483 8.900 Z"/><circle cx="12" cy="12" r="3.2"/>'};return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${shapes[name]||shapes.cameras}</svg>`}
function nav(){const pages=[['dashboard','Dashboard'],['cameras','Kameras'],['editor','Stream-Editor'],['streams','Streams'],['system','System']];return `<aside><div class="brand"><img src="/logo.svg" alt="Spherelay"><div><strong>Spherelay</strong><small>STREAM STUDIO</small></div></div><div class="nav">${pages.map(([id,label])=>`<button class="${model.page===id?'active':''}" data-action="page" data-value="${id}"><span class="glyph">${icon(id)}</span>${label}</button>`).join('')}</div><div class="asidefoot"><div class="online">● Spherelay ${esc(model.state?.version)}</div><div>Lokale Verwaltung</div><div class="minor">127.0.0.1:8787</div><button class="btn sm soft" data-action="logout" style="margin-top:10px">Abmelden</button></div></aside>`}
function pageTop(title,subtitle,actions=''){return `<header class="top"><div><div class="eyebrow">Spherelay · ${esc(model.page)}</div><h1>${title}</h1><p>${subtitle}</p></div><div class="right">${actions}</div></header>`}
function cameraCard(c){
  const n=views().filter(v=>v.cameraId===c.id).length;
  const running=Boolean(model.state.processes?.[c.id]?.running);
  return `<div class="item camera-row" data-camera-health="${esc(c.id)}"><div class="avatar">${icon('cameras')}</div>
    <div class="iteminfo"><strong>${esc(c.name)}</strong><small>${esc(c.url||'RTSP-Quelle')} · ${n} Ansichten</small><small data-source-error></small>${pendingPipelineNotice(c.id)}</div>
    <span class="pill ${running?'ok':''}">${running?'Aktiv':'Bereit'}</span>
    <button class="btn sm" data-action="edit-camera" data-value="${esc(c.id)}">Bearbeiten</button>
    <button class="btn sm primary" data-action="open-editor" data-value="${esc(c.id)}">Editor</button>
    ${trashButton('delete-camera',c.id,'Kamera löschen')}</div>`;
}
function dashboard(){
  const state=model.state;
  const cameras=camList();
  const running=cameras.filter(camera=>state.processes?.[camera.id]?.running).length;
  const missing=[['ffmpeg','FFmpeg','Videocodierung'],['mediamtx','MediaMTX','RTSP-Ausgabe']].filter(([id])=>!state[id]);
  const setup=missing.length?`<section class="panel"><div class="panelhead"><h2>Einrichtung</h2></div><div class="list">
    ${missing.map(([,name,detail])=>`<div class="item"><div class="iteminfo"><strong>${name} installieren</strong><small>${detail}</small></div></div>`).join('')}
    </div><div class="panelbody"><button class="btn" data-action="page" data-value="system">Komponenten öffnen</button></div></section>`:'';
  const cameraList=`<section class="panel"><div class="panelhead"><h2>Kameras</h2><button class="btn sm" data-action="page" data-value="cameras">Alle anzeigen</button></div>
    <div class="list">${cameras.length?cameras.map(cameraCard).join(''):'<div class="empty">Füge eine Kamera per Netzwerksuche oder RTSP-Adresse hinzu.</div>'}</div></section>`;
  return pageTop('Dashboard','Kameras, virtuelle Streams und Systemstatus','<button class="btn primary" data-action="add-camera">+ Kamera hinzufügen</button>')+
    `<div class="stats"><div class="stat"><small>Quellkameras</small><strong>${cameras.length}</strong></div>
    <div class="stat"><small>Virtuelle Streams</small><strong>${views().length}</strong></div>
    <div class="stat"><small>Aktive Quellen</small><strong>${running}</strong><span>${state.mediaRunning?'MediaMTX läuft':'MediaMTX ist aus'}</span></div>
    <div class="stat"><small>Komponenten</small><strong>${2-missing.length}/2</strong><span>FFmpeg + MediaMTX</span></div></div>`+
    (setup?`<div class="cols">${cameraList}${setup}</div>`:cameraList);
}
function discoveryResults(){
 const report=model.discoveryReport;
 const labels={'camera':'Kamera','camera-hint':'Kamerahinweis','video-device':'Videogerät'};
 const count=category=>model.discover.filter(device=>device.category===category).length;
 const list=model.discover.filter(discoveryMatchesFilters).map(d=>{
   const known=cameraForDiscovery(d); const manufacturer=cameraBrand(d.details?.manufacturer||d.manufacturer);
   const name=d.details?.model||d.model||'Modell unbekannt';
   const firmware=d.details?.firmware||d.firmware;
   const loginNote=d.authStatus==='failed'?d.authMessage:d.authRequired?'Kamerazugang erforderlich':'';
   return `<article class="discovery-card">
     <div class="discovery-card-head"><div class="avatar discovery-icon">${cameraProductVisual()}</div>
     <div class="discovery-identity"><small>${esc(manufacturer||'Hersteller unbekannt')}</small><strong>${esc(name)}</strong>${known?`<small>Bereits hinzugefügt</small>`:''}</div>
     <span class="pill ${d.category==='camera'?'ok':'warn'}">${esc(labels[d.category]||'Gerät')}</span></div>
     <dl class="discovery-values"><div><dt>IP-Adresse</dt><dd class="mono">${esc(d.address)}</dd></div>
       ${firmware?`<div><dt>Firmware</dt><dd>${esc(firmware)}</dd></div>`:''}
       ${d.details?.sources?.length?`<div><dt>Quellstreams</dt><dd>${d.details.sources.length}</dd></div>`:''}</dl>
     ${loginNote?`<p>${esc(loginNote)}</p>`:''}
     <div class="discovery-card-actions"><details><summary>Erkennung</summary><p>${esc(d.method||'')}${d.evidence?' · '+esc(d.evidence):''}</p></details>
     ${known?`<button class="btn sm" data-action="edit-camera" data-value="${esc(known.id)}" title="Bereits hinzugefügt">Kamera öffnen</button>`:`<button class="btn sm" data-action="camera-details" data-value="${esc(d.address)}">Einrichten</button>`}</div>
   </article>`;
 }).join('');
 const others=report?.otherWebDevices||[];
 return (report?`<dl class="discovery-summary" aria-label="Suchergebnis"><div><dt>Kameras</dt><dd>${count('camera')}</dd></div><div><dt>Kamerahinweise</dt><dd>${count('camera-hint')}</dd></div><div><dt>Weitere Videogeräte</dt><dd>${count('video-device')}</dd></div><div><dt>Prüfziele</dt><dd>${report.checked} / ${report.total}</dd></div></dl>
 <div class="search-summary ${report.canceled?'search-incomplete':''}"><strong>${report.canceled?'Suche abgebrochen':'Suche abgeschlossen'}</strong><span>${esc((report.networks||[]).map(n=>n.cidr).join(', '))}</span></div>${report.warnings?.length?`<details class="discovery-notices"><summary>${report.warnings.length} Hinweise</summary><ul>${report.warnings.map(note=>`<li>${esc(note)}</li>`).join('')}</ul></details>`:''}`:'')+
 `<div class="discovery-grid sectiongap" aria-busy="${!!model.discoveryBusy}">${list||`<div class="empty">${model.discoveryBusy?'Netzwerk wird durchsucht …':report?'Keine Kameras erkannt. Prüfe Netzwerkbereich und Ports.':'Starte die Suche, um Kameras im lokalen Netzwerk zu finden.'}</div>`}</div>`+
 (others.length?`<details class="discovery-other sectiongap"><summary>${others.length} weitere Webgeräte</summary>${others.map(d=>`<div class="item"><div class="iteminfo"><strong class="mono">${esc(d.address)}</strong><small>${esc(d.url)} · HTTP ${d.status}</small></div></div>`).join('')}</details>`:'');
}
function cameraPage(){
  return pageTop('Kameras','Quellen erkennen, hinzufügen und überprüfen','<button class="btn primary" data-action="add-camera">+ Kamera hinzufügen</button>')+
    `<section class="panel"><div class="panelhead"><h2>Konfigurierte Kameras</h2><span class="muted">${camList().length} Quellen</span></div>${camList().length?camList().map(cameraCard).join(''):'<div class="empty">Keine Kameras vorhanden.</div>'}</section>`+
    networkSearchPanel();
}
async function discoverCameras(){
 if(model.discoveryBusy)return;
 const searchStartedAt=Date.now();
 const network=document.getElementById('discoveryNetwork')?.value.trim()||'';
 const methods=[...document.querySelectorAll('[data-discovery-method]:checked')].map(input=>input.value);if(!methods.length)throw Error('Wähle mindestens ein Suchverfahren.');model.discoveryMethods=methods;const rawPorts=document.getElementById('discoveryPorts')?.value||'80,443';
 const ports=rawPorts.split(',').map(x=>Number(x.trim()));
 if(methods.includes('vendor')&&(!ports.length||ports.length>4||ports.some(x=>!Number.isInteger(x)||x<1||x>65535)))throw Error('Bitte bis zu vier gültige Ports durch Kommas getrennt eingeben.');
 model.discoveryNetwork=network;model.discoveryPorts=rawPorts;model.discoveryBusy=true;model.page='cameras';render();
 const poll=setInterval(async()=>{try{const p=await api('discover/progress');const label=document.getElementById('discoveryProgress');if(label&&p.running&&model.discoveryBusy)label.textContent=p.phase+(p.total?' · '+p.checked+' / '+p.total+' Ziele':'');}catch{}},600);
 try{model.discoveryReport=await api('discover','POST',{network,methods,ports:methods.includes('vendor')?ports:[],useCredentials:!!model.discoveryCredentialId,credentialId:model.discoveryCredentialId});model.discover=model.discoveryReport.devices||[];model.discoveryStartedAt=searchStartedAt;notify((model.discoveryReport.canceled?'Suche abgebrochen: ':'Suche abgeschlossen: ')+model.discover.length+' Geräte gefunden');}
 finally{clearInterval(poll);model.discoveryBusy=false;render()}
}
function slider(key,title,value,min,max,step){return `<div class="sliderrow"><div class="toastline"><label for="slide-${key}">${title}</label><output id="out-${key}">${Number(value).toFixed(0)}${key==='zoom'?'':'°'}</output></div><input id="slide-${key}" type="range" min="${min}" max="${max}" step="${step}" value="${value}" data-slider="${key}"></div>`}
function streamAddress(id){return 'rtsp://127.0.0.1:8555/'+encodeURIComponent(id)}

function streamsPage(){
  const rows=views().map((stream,index)=>{
    const camera=camList().find(item=>item.id===stream.cameraId);
    const status=model.health?.get(stream.id);
    return `<div class="item stream-row" data-stream-status="${esc(stream.id)}">
      <div class="avatar" style="color:${color(index)}">${icon('streams')}</div>
      <div class="iteminfo"><strong>${esc(stream.name)}</strong>
        <small>${esc(camera?.name||'–')} · ${stream.width} × ${stream.height} · ${stream.fps} FPS</small>
        <div class="mono stream-address">${esc(streamAddress(stream.id))}</div>
        <small data-stream-detail>RTSP-Status wird geprüft …</small>${pendingPipelineNotice(stream.cameraId)}</div>
      <div class="stream-row-actions"><span class="stream-badge ${esc(status?.state||'')}" data-stream-badge>${streamStateLabels[status?.state]||'Wird geprüft'}</span>
        <label class="stream-switch"><input type="checkbox" data-stream-enabled data-view="${esc(stream.id)}" ${stream.enabled?'checked':''}>Aktiv</label>
        ${streamActions(stream)}
        <button class="btn sm" data-action="copy-url" data-value="${esc(stream.id)}">Kopieren</button>
        <button class="btn sm" data-action="edit-stream" data-value="${esc(stream.id)}">Editor</button>
        ${trashButton('delete-stream',stream.id,'Ansicht löschen')}</div>
    </div>`;
  }).join('');
  return pageTop('Ausgabestreams','RTSP-Adressen und Status deiner virtuellen Ansichten','<button class="btn" data-action="reload">Aktualisieren</button>')+
    `<section class="panel"><div class="panelhead"><h2>Virtuelle Streams</h2><span>${views().length}</span></div>${rows||'<div class="empty">Noch keine virtuellen Streams angelegt.</div>'}</section>`;
}
const logLevelLabels={warn:'Warnung',info:'Info',debug:'Debug'};
const diagnosisTabs=[['events','Ereignisse'],['app','Spherelay'],['mediamtx','MediaMTX'],['ffmpeg','FFmpeg']];
function diagnosticsPanel(){
  const source=model.diagnosisTab||'events';
  const options=Object.entries(logLevelLabels).map(([id,label])=>`<option value="${id}">${label}</option>`).join('');
  const cameraId=model.diagnosisCameraId||camList()[0]?.id||'';
  const label=diagnosisTabs.find(([id])=>id===source)[1];
  const controls=source==='app'?'':`<div class="formgrid">
    <div class="field"><label for="logLevel">${label} · Logstufe</label><select id="logLevel" disabled>${options}</select><small id="logLevelState"></small></div>
    ${source==='ffmpeg'?`<div class="field"><label for="logCamera">Kamera</label><select id="logCamera" ${camList().length?'':'disabled'}>${camList().map(c=>`<option value="${esc(c.id)}" ${c.id===cameraId?'selected':''}>${esc(c.name)}</option>`).join('')||'<option value="">Keine Kamera vorhanden</option>'}</select></div>`:''}
    </div><div class="toolbar sectiongap">
    <button class="btn sm" data-action="save-log-levels" disabled>Logstufe speichern</button>
    <button class="btn sm" data-action="${source==='mediamtx'?'restart-media':'restart-ffmpeg'}" ${source==='ffmpeg'&&!camList().length?'disabled':''}>${label} neu starten</button>
    </div>`;
  return `<section class="panel diagnosis-panel"><div class="panelhead"><h2>Diagnoseprotokoll</h2><button class="btn sm" data-action="logs">Aktualisieren</button></div>
    <div class="editor-tabs" role="tablist" aria-label="Protokollquellen">${diagnosisTabs.map(([id,name])=>`<button id="diagnosis-tab-${id}" data-action="diagnosis-tab" data-value="${id}" role="tab" aria-controls="diagnosis-panel" aria-selected="${id===source}" tabindex="${id===source?0:-1}">${name}</button>`).join('')}</div>
    <div class="panelbody" id="diagnosis-panel" role="tabpanel" aria-labelledby="diagnosis-tab-${source}">${source==="events"?diagnosticsEventsPanel():controls+"<div class=\"logs sectiongap\" id=\"logsContent\">Protokoll wird geladen …</div>"}</div></section>`;
}

function showDiagnosisTab(source,focus=false){
  if(!diagnosisTabs.some(([id])=>id===source))return;
  model.diagnosisTab=source;
  document.getElementById('system-panel-diagnostics').innerHTML=diagnosticsPanel();
  if(focus)document.getElementById('diagnosis-tab-'+source).focus();
  updateDiagnosis();
}

document.addEventListener('keydown',event=>{
  const tab=event.target.closest('[data-action="diagnosis-tab"]');
  if(!tab||!['ArrowLeft','ArrowRight','Home','End'].includes(event.key))return;
  event.preventDefault();
  const index=diagnosisTabs.findIndex(([id])=>id===tab.dataset.value);
  const next=event.key==='Home'?0:event.key==='End'?diagnosisTabs.length-1:(index+(event.key==='ArrowRight'?1:diagnosisTabs.length-1))%diagnosisTabs.length;
  showDiagnosisTab(diagnosisTabs[next][0],true);
});

let streamPollTimer;
let streamPollRequest;
let diagnosisPollTimer;
function stopStreamPolling(){
  clearTimeout(streamPollTimer);
  clearInterval(diagnosisPollTimer);
  streamPollRequest?.abort();
}

async function pollStreamStatuses(){
  const rows=[...root.querySelectorAll('[data-stream-status]')];
  const sources=[...root.querySelectorAll('[data-camera-health]')];
  if(!rows.length&&!sources.length)return;
  const request=new AbortController();
  streamPollRequest=request;
  try{
    const response=await fetch('/api/streams/status',{signal:request.signal,cache:'no-store'});
    if(!response.ok)throw Error('Statusabfrage fehlgeschlagen');
    const data=await response.json();
    if(request.signal.aborted)return;
    model.health=new Map(data.streams.map(status=>[status.id,status]));
    const sourceErrors=new Map();
    for(const status of data.streams){
      if(status.sourceError)sourceErrors.set(status.cameraId,status);
    }
    const time=value=>new Date(value).toLocaleTimeString();
    for(const row of sources){
      const status=sourceErrors.get(row.dataset.cameraHealth);
      row.querySelector('[data-source-error]').textContent=status?
        [status.sourceError,status.nextRetry&&('Nächster Versuch '+time(status.nextRetry))].filter(Boolean).join(' · '):'';
    }
    for(const row of rows){
      const status=model.health.get(row.dataset.streamStatus);
      const badge=row.querySelector('[data-stream-badge]');
      badge.textContent=streamStateLabels[status?.state]||'Unbekannt';
      badge.classList.remove('online','offline','unhealthy');
      if(status)badge.classList.add(status.state);
      const diagnostic=row.querySelector('[data-stream-diagnostic]');
      if(diagnostic)diagnostic.hidden=status?.state!=='unhealthy';
      const detail=status?.online?'RTSP-Videospur erreichbar':status?.sourceError?'Kameraquelle nicht verfügbar':status?.detail;
      row.querySelector('[data-stream-detail]').textContent=status?[
        detail,
        status.lastOnline&&!status.online&&('Zuletzt online '+time(status.lastOnline)),
        status.nextRetry&&('Verbindet erneut ab '+time(status.nextRetry)),
        status.checkedAt&&('Geprüft '+time(status.checkedAt))
      ].filter(Boolean).join(' · '):'Kein Prüfergebnis';
    }
  }catch{
    if(request.signal.aborted)return;
    for(const row of rows){
      const badge=row.querySelector('[data-stream-badge]');
      badge.textContent='Unbekannt';
      badge.classList.remove('online','offline','unhealthy');
      row.querySelector('[data-stream-detail]').textContent='Statusabfrage fehlgeschlagen';
    }
  }finally{
    if(!request.signal.aborted)streamPollTimer=setTimeout(pollStreamStatuses,5000);
  }
}
let diagnosisRequest=0;
async function refreshDiagnosis(force=false){
  if(document.getElementById('diagnosticRows')&&model.systemTab==='diagnostics')return refreshDiagnosticEvents(force);
  const content=document.getElementById('logsContent');
  if(!content||model.systemTab!=='diagnostics')return;
  const source=model.diagnosisTab||'events';
  const cameraId=document.getElementById('logCamera')?.value||'';
  const request=++diagnosisRequest;
  const query=new URLSearchParams({component:source,cameraId});
  const status=await api(source==='app'?'logs':'logs/components?'+query);
  if(!content.isConnected||request!==diagnosisRequest)return;
  if(source!=='app'){
    const level=document.getElementById('logLevel');
    const save=document.querySelector('[data-action="save-log-levels"]');
    const configured=status.levels[source];
    const active=source==='mediamtx'?status.mediaActive:status.pipelines[cameraId];
    level.dataset.saved=configured;
    if(!level.dataset.changed)level.value=configured;
    level.disabled=false;
    save.disabled=!level.dataset.changed||save.dataset.saving==='true';
    document.getElementById('logLevelState').textContent=active?
      'Aktiv: '+logLevelLabels[active]+(active!==configured?' · Neustart erforderlich':''):
      (source==='mediamtx'?'MediaMTX ist gestoppt':cameraId?'Kamera-Pipeline gestoppt':'Keine Kamera vorhanden');
  }
  const value=(source==='app'?status.lines?.join('\n'):status.content)||'Keine Einträge vorhanden.';
  if(content.textContent!==value){
    const atEnd=content.scrollHeight-content.scrollTop-content.clientHeight<8;
    content.textContent=value;
    if(atEnd)content.scrollTop=content.scrollHeight;
  }
}
function promptMediaRestart(later=false){
  const dialog=showDialog('MediaMTX jetzt neu starten?', '<p>Alle RTSP-Streams werden kurz unterbrochen.</p>','Jetzt neu starten',async()=>{
    const result=await api('server/restart','POST');
    notify(result.message);
  });
  if(later)dialog.querySelector('#dialogCancel').textContent='Später';
}
function promptFFmpegRestart(cameraIds=null){
  const ids=cameraIds||[document.getElementById('logCamera')?.value].filter(Boolean);
  const cameras=camList().filter(camera=>ids.includes(camera.id));
  if(!cameras.length)return;
  const dialog=showDialog('FFmpeg neu starten?',`<p>${cameras.map(camera=>esc(camera.name)).join(', ')}. Laufende Ansichten dieser Kameras werden kurz unterbrochen.</p>`,'Jetzt neu starten',async()=>{
    for(const camera of cameras)await api('pipeline/restart','POST',{cameraId:camera.id});
    notify('FFmpeg neu gestartet.');
  });
  dialog.querySelector('#dialogCancel').textContent='Später';
}
async function saveLogLevels(){
  const source=model.diagnosisTab;
  const level=document.getElementById('logLevel');
  const save=document.querySelector('[data-action="save-log-levels"]');
  if(!level||!save||!level.dataset.changed)return;
  const selected=level.value;
  save.dataset.saving='true';
  save.disabled=true;
  level.disabled=true;
  try{
    await api('logs/components','POST',{[source]:selected});
    delete level.dataset.changed;
    level.dataset.saved=selected;
    notify('Logstufe gespeichert');
    const component=await api('logs/components?'+new URLSearchParams({component:source,cameraId:document.getElementById('logCamera')?.value||''}));if(source==='mediamtx'&&component.mediaActive&&component.mediaActive!==selected)promptMediaRestart(true);if(source==='ffmpeg')promptFFmpegRestart(Object.keys(component.pipelines).filter(id=>component.pipelines[id]!==selected));
  }finally{
    delete save.dataset.saving;
    level.disabled=false;
    save.disabled=!level.dataset.changed;
    if(level.isConnected)updateDiagnosis();
  }
}
function updateDiagnosis(){
  const content=document.getElementById('logsContent');
  refreshDiagnosis().catch(()=>{
    if(content?.isConnected)content.textContent='Diagnoseprotokoll konnte nicht geladen werden.';
  });
}
function render(){stopStreamPolling();stopEditorPreview();if(!model.state)return;rememberRoute();const pages={dashboard,cameras:cameraPage,editor:editorPage,streams:streamsPage,system:systemPage};root.innerHTML=`<div class="layout">${nav()}<main class="main">${(pages[model.page]||dashboard)()}</main></div>`;if(model.page==='editor'){fitEditorWorkspace();restoreEditorThumbnails();refreshImages()}if(model.page==='system'){pollComponents();refreshWindowsIntegration();updateDiagnosis();diagnosisPollTimer=setInterval(updateDiagnosis,5000)}if(['streams','cameras','dashboard'].includes(model.page))pollStreamStatuses()}
function showDialog(title,body,saveText,onSave){document.getElementById('dialogModal')?.remove();const dia=document.createElement('dialog');dia.id='dialogModal';dia.style.cssText='border:1px solid #3d5a67;border-radius:16px;background:#14232e;color:#ecf5f6;padding:22px;width:min(540px,calc(100% - 26px));box-shadow:0 20px 80px #000b';dia.innerHTML=`<form id="dialogForm"><h2>${title}</h2><div style="margin:17px 0">${body}</div><div class="toolbar" style="justify-content:flex-end"><button class="btn" type="button" id="dialogCancel">Abbrechen</button><button class="btn primary" type="submit">${saveText}</button></div></form>`;document.body.appendChild(dia);dia.showModal();dia.querySelector('#dialogCancel').onclick=()=>dia.close();dia.querySelector('form').onsubmit=async e=>{e.preventDefault();try{const after=await onSave(dia);dia.close();await loadState();if(typeof after==="function")after()}catch(ex){notify(ex.message,true)}};dia.addEventListener('close',()=>dia.remove());return dia}

async function refreshCredentialMetadata(){model.discoveryCredentials=await api('discovery/credentials');if(!model.discoveryCredentials.profiles.some(p=>p.id===model.discoveryCredentialId))model.discoveryCredentialId=model.discoveryCredentials.profiles[0]?.id||'';}
function credentialManager(editId='',confirmId=''){
 document.getElementById('credentialManager')?.remove();
 const profiles=model.discoveryCredentials?.profiles||[];const edit=profiles.find(p=>p.id===editId);
 const dialog=document.createElement('dialog');dialog.id='credentialManager';dialog.className='access-dialog';
 dialog.innerHTML=`<div class="panelhead"><h2>Kamerazugänge verwalten</h2><button class="btn soft" data-profile-action="close">Schließen</button></div><p>Mehrere Zugänge mit Beschreibung speichern. Für jede Suche wird ein Zugang gezielt ausgewählt; es werden keine Passwortlisten an Kameras ausprobiert.</p><div class="access-list">${profiles.map(p=>`<div class="item"><div class="iteminfo"><strong>${esc(p.description)}</strong><small>${esc(p.username)} · Passwort verschlüsselt gespeichert</small></div><button class="btn sm" data-profile-action="edit" data-id="${esc(p.id)}">Bearbeiten</button><button class="btn sm" data-profile-action="${confirmId===p.id?'delete-confirm':'delete'}" data-id="${esc(p.id)}">${confirmId===p.id?'Endgültig entfernen':'Entfernen'}</button></div>`).join('')||'<div class="empty">Noch keine Zugänge gespeichert.</div>'}</div><form id="profileForm"><h3>${edit?'Zugang bearbeiten':'Weiteren Zugang hinzufügen'}</h3><div class="field sectiongap"><label for="profileDescription">Beschreibung</label><input id="profileDescription" required maxlength="160" placeholder="z. B. Camera 01 – Lesekonto" value="${esc(edit?.description||'')}"></div><div class="formgrid sectiongap"><div class="field"><label for="profileUsername">Kamerabenutzer</label><input id="profileUsername" required maxlength="128" autocomplete="off" value="${esc(edit?.username||'')}"></div><div class="field"><label for="profilePassword">Kamerapasswort</label><input id="profilePassword" type="password" autocomplete="new-password" ${edit?'':'required'} placeholder="${edit?'Leer lassen zum Beibehalten':'Passwort'}"></div></div><div class="toolbar sectiongap"><button class="btn primary" type="submit">Zugang speichern</button>${edit?'<button class="btn" type="button" data-profile-action="new">Neuer Zugang</button>':''}</div><p>Auf diesem Windows-PC verschlüsselt gespeichert. Passwörter werden nicht erneut angezeigt.</p></form>`;
 document.body.appendChild(dialog);dialog.showModal();
 dialog.addEventListener('close',()=>dialog.remove());
 dialog.addEventListener('click',async e=>{const button=e.target.closest('[data-profile-action]');if(!button)return;e.stopPropagation();const action=button.dataset.profileAction,id=button.dataset.id;try{if(action==='close'){dialog.close();return}if(action==='edit'){credentialManager(id);return}if(action==='new'){credentialManager();return}if(action==='delete'){credentialManager(editId,id);return}if(action==='delete-confirm'){await api('discovery/credentials','DELETE',{id});await refreshCredentialMetadata();render();credentialManager();notify('Zugang entfernt')}}catch(error){notify(error.message,true)}});
 dialog.querySelector('form').onsubmit=async e=>{e.preventDefault();try{const input={id:edit?.id||'',description:dialog.querySelector('#profileDescription').value,username:dialog.querySelector('#profileUsername').value,password:dialog.querySelector('#profilePassword').value};model.discoveryCredentials=await api('discovery/credentials','POST',input);dialog.querySelector('#profilePassword').value='';model.discoveryCredentialId=edit?.id||model.discoveryCredentials.profiles.at(-1)?.id||'';render();credentialManager();notify('Zugang verschlüsselt gespeichert')}catch(error){notify(error.message,true)}};
}
function cameraBrand(manufacturer){
 const brand=(manufacturer||'').replace(/\s*\(Modellkennung\)$/i,'').trim();
 return /^hikvision$/i.test(brand)?'Hikvision':brand;
}
function cameraProductVisual(){
 return `<span class="product-visual" aria-hidden="true">${icon('cameras')}</span>`;
}
document.body.addEventListener('error',e=>{if(e.target.matches?.('.product-visual img'))e.target.remove()},true);

async function getCameraDetails(address){
 const device=model.discover.find(d=>d.address===address);if(!device)return;if(device.method==='RTSP'&&device.rtspAddress){cameraDialog(null,{url:device.rtspAddress,name:device.model});return}
 const profiles=model.discoveryCredentials?.profiles||[];
 const credentialId=model.discoveryCredentialId||(profiles.length===1?profiles[0].id:'');
 if(!credentialId){notify(profiles.length?'Bitte einen gespeicherten Kamerazugang auswählen.':'Bitte einen Kamerazugang hinterlegen.',true);if(!profiles.length)credentialManager();return}
 if(device.details?.credentialId===credentialId&&Date.now()-(device.detailsAt||model.discoveryStartedAt)<19*60*1000){
   cameraImportDialog(device.details,device);return;
 }
 notify('Geräteinformationen und Quellstreams werden abgefragt …');
 const base=new URL(device.xaddr).origin;
 device.details=await api('discovery/details','POST',{base,credentialId});
 device.detailsAt=Date.now();
 render();cameraImportDialog(device.details,device);
}
function cameraImportDialog(details,device){
 const sources=details.sources||[];
 const manufacturer=cameraBrand(details.manufacturer||device.manufacturer);
 const name=details.model||device.model||'Modell unbekannt';
 const metadata=[details.deviceName,details.firmware&&('Firmware '+details.firmware)].filter(Boolean);
 const header=`<div class="camera-product"><div class="camera-product-image">${cameraProductVisual()}</div>
   <div><small>${esc(manufacturer)}</small><h3>${esc(name)}</h3><small>${metadata.map(esc).join(' · ')}</small></div></div>`;
 const warnings=(details.warnings||[]).map(w=>`<p>${esc(w)}</p>`).join('');
 if(!sources.length){showDialog('Kamera hinzufügen',header+warnings+'<p>Die Kamera meldet keine auswertbaren Quellstreams.</p>','Schließen',async()=>{});return}
 const recommended=[...sources].sort((a,b)=>(b.width*b.height)-(a.width*a.height))[0];
 const streamLabel=s=>[s.name||'Stream '+s.id,s.codec!==s.name?s.codec:'',s.width&&s.height?s.width+' × '+s.height:'',s.fps?s.fps+' FPS':''].filter(Boolean).join(' · ');
 const dialog=showDialog('Kamera hinzufügen',`${header}<div class="field sectiongap"><label for="importSource">Quellstream</label>
   <select id="importSource">${sources.map(s=>`<option value="${esc(s.id)}" ${s.id===recommended.id?'selected':''}>${esc(streamLabel(s))}</option>`).join('')}</select>
   <div class="source-card" id="importSourceDetails"></div></div>${warnings}
   <div class="field sectiongap"><label for="importName">Kameraname</label><input id="importName" value="Camera ${String(camList().length+1).padStart(2,'0')}" required maxlength="80"></div>
   <div class="field sectiongap"><label for="importProjection">Quellprojektion</label><select id="importProjection"><option value="fisheye">Fisheye (rundes Bild)</option><option value="equirect">Equirectangular (360° Panorama)</option></select></div>`,
   'Kamera hinzufügen',async dialog=>{
     await api('discovery/import','POST',{inspectionId:details.inspectionId,sourceId:dialog.querySelector('#importSource').value,name:dialog.querySelector('#importName').value,projection:dialog.querySelector('#importProjection').value});
     notify('Kamera mit ermitteltem Quellstream hinzugefügt');
   });
 const select=dialog.querySelector('#importSource'),card=dialog.querySelector('#importSourceDetails');
 const update=()=>{
   const source=sources.find(s=>s.id===select.value);
   card.innerHTML=source?`<small>${esc(source.status)}</small><div class="mono">${esc(source.url)}</div>`:'';
 };
 select.addEventListener('change',update);
 update();
}

function cameraDialog(camera=null,suggestion=null){
  const modal=showDialog(camera?'Kamera bearbeiten':'Kamera hinzufügen',`<div class="formgrid">
    <div class="field full"><label for="camName">Name</label><input id="camName" value="${esc(camera?.name||suggestion?.name||('Kamera '+String(camList().length+1).padStart(2,'0')))}" required maxlength="80"></div>
    <div class="field full"><label for="camURL">RTSP-Adresse ${camera?'(leer lassen für die bisherige Adresse)':''}</label><input id="camURL" value="${esc(suggestion?.url||'')}" placeholder="rtsp://192.168.1.100:554/…" ${camera?'':'required'} autocomplete="off"></div>
    <div class="field full"><label for="camProj">Quellprojektion</label><select id="camProj"><option value="fisheye" ${camera?.projection!=='equirect'?'selected':''}>Fisheye (rundes Bild)</option><option value="equirect" ${camera?.projection==='equirect'?'selected':''}>Equirectangular (360° Panorama)</option></select></div>
    </div><p id="cameraTestResult" class="camera-test-result" role="status"></p><button type="button" class="btn" id="testCameraConnection">Verbindung testen</button>`,
    camera?'Speichern':'Kamera erstellen',async d=>{
      const saved=await api('cameras','POST',{id:camera?.id||'',name:d.querySelector('#camName').value,url:d.querySelector('#camURL').value,projection:d.querySelector('#camProj').value});
      return saved.restartRequired?()=>promptCameraRestart(saved.id):null;
    });
  const button=modal.querySelector('#testCameraConnection'), status=modal.querySelector('#cameraTestResult');const footer=modal.querySelector('form > .toolbar');footer.classList.add('camera-dialog-actions');footer.prepend(button);
  button.addEventListener('click',async()=>{
    button.disabled=true;status.textContent='Verbindung wird geprüft …';
    try{
      const value=modal.querySelector('#camURL').value.trim();
      const result=await api('cameras/test','POST',{id:camera?.id||'',url:value});
      status.textContent=result.ok?'Kamera erreichbar. Eine Videospur wurde gefunden. Die Wiedergabe wurde noch nicht geprüft.':'Verbindung konnte nicht bestätigt werden. '+result.detail;status.dataset.result=result.ok?'success':'error';
    }catch(err){status.textContent=err.message}
    finally{button.disabled=false}
  });
}
async function makeView(){if(!selectedCam()){notify('Bitte Kamera auswählen',true);return};const n=camViews().length;let v={id:'',cameraId:model.cameraId,name:'Ansicht '+(n+1),yaw:n===0?0:Math.min(60,n*20),pitch:0,fov:80,width:960,height:720,fps:10,enabled:true};const result=await api('streams','POST',v);model.viewId=result.id;await loadState()}
function updateOverlay(){updateLiveOverlay();drawEditorViews()}
function refreshImages(){updateOverlay();startEditorPreview()}
function coordToAngles(event){return liveClickAngles(event)}
function setCoords(ev){let yaw=document.getElementById('slide-yaw'),pitch=document.getElementById('slide-pitch');if(!yaw||!pitch)return;let a=coordToAngles(ev);yaw.value=((a.yaw+540)%360)-180;pitch.value=a.pitch;document.getElementById('out-yaw').textContent=yaw.value+'°';document.getElementById('out-pitch').textContent=pitch.value+'°';markEditorChanged();updateOverlay()}
async function saveEditorDraft(){
  const v=selectedView();
  if(!v)return false;
  const [width,height]=document.getElementById('viewResolution').value.split('x').map(Number);
  const changed={
    ...previewDraft(v),osd:readOSD(),width,height,
    roll:Number(document.getElementById('slide-roll').value),
    name:document.getElementById('viewName').value,
    fps:Number(document.getElementById('viewFPS').value),
    go1984Compatibility:document.getElementById('go1984Compatibility').checked,
    yaw:Number(document.getElementById('slide-yaw').value),
    pitch:Number(document.getElementById('slide-pitch').value),
    fov:Number(document.getElementById('slide-fov').value)
  };
  const saved=await api('streams','POST',changed);model.savedRestartCamera=saved.restartRequired?v.cameraId:'';
  const index=views().findIndex(view=>view.id===v.id);
  if(index>=0)views()[index]=changed;
  invalidateEditorThumbnails();
  model.changed=false;
  const status=document.getElementById('editorDraftStatus');
  if(status)status.textContent='Gespeichert';
  notify('Sichtfeld gespeichert');
  return true;
}

function confirmEditorLeave(){
  if(model.page!=='editor'||!model.changed)return Promise.resolve(true);
  if(document.getElementById('unsavedEditorDialog'))return Promise.resolve(false);
  return new Promise(resolve=>{
    const dialog=document.createElement('dialog');
    dialog.id='unsavedEditorDialog';
    dialog.className='editor-confirmation';
    dialog.setAttribute('aria-labelledby','unsavedEditorTitle');
    dialog.innerHTML=`<h2 id="unsavedEditorTitle">Ungespeicherte Änderungen</h2>
      <p>Möchtest du die Änderungen an dieser Ansicht speichern?</p>
      <p class="notice" data-save-error hidden role="alert"></p>
      <div class="toolbar sectiongap">
        <button class="btn" data-choice="cancel" autofocus>Abbrechen</button>
        <button class="btn" data-choice="discard">Ohne Speichern fortfahren</button>
        <button class="btn primary" data-choice="save">Jetzt speichern</button>
      </div>`;
    let proceed=false;
    let saving=false;
    dialog.addEventListener('cancel',event=>{if(saving)event.preventDefault()});
    dialog.addEventListener('close',()=>{dialog.remove();resolve(proceed)},{once:true});
    dialog.addEventListener('click',async event=>{
      const choice=event.target.closest('[data-choice]')?.dataset.choice;
      if(!choice||saving)return;
      if(choice==='save'){
        saving=true;
        const buttons=[...dialog.querySelectorAll('button')];
        for(const button of buttons)button.disabled=true;
        try{
          if(!await saveEditorDraft())throw Error('Die aktuelle Ansicht konnte nicht gespeichert werden.');
        }catch(error){
          const message=dialog.querySelector('[data-save-error]');
          message.textContent=error.message;
          message.hidden=false;
          return;
        }finally{
          saving=false;
          for(const button of buttons)button.disabled=false;
        }
      }
      proceed=choice!=='cancel';
      dialog.close();
    });
    document.body.appendChild(dialog);
    dialog.showModal();
  });
}

function confirmCameraDelete(id){
  const camera=camList().find(item=>item.id===id);
  if(!camera)return;
  const count=views().filter(view=>view.cameraId===id).length;
  const detail=count===1?'Die zugehörige Ansicht wird ebenfalls gelöscht.':count>1?`Die ${count} zugehörigen Ansichten werden ebenfalls gelöscht.`:'Dieser Kamera sind keine Ansichten zugeordnet.';
  showDialog('Kamera löschen',`<p>„${esc(camera.name)}“ dauerhaft aus Spherelay entfernen?</p><p>${detail} Eine laufende Stream-Ausgabe dieser Kamera wird beendet.</p>`,'Kamera löschen',async()=>{
    await api('cameras/'+encodeURIComponent(id),'DELETE');
    invalidateEditorThumbnails();
    notify('Kamera gelöscht');
  });
}
function askConfirm(message,doIt){showDialog('Aktion bestätigen',`<p style="font-size:14px">${esc(message)}</p>`, 'Bestätigen', async()=>{await doIt()})}
document.addEventListener('click',async event=>{if(event.target.closest('[data-fullscreen]'))return;let el=event.target.closest('[data-action]');if(!el)return;const action=el.dataset.action,value=el.dataset.value;try{if(['page','reload','logout','open-editor','new-stream','select-view','edit-stream','delete-stream','start-pipeline','stop-pipeline'].includes(action)&&!await confirmEditorLeave())return;switch(action){case 'page':await navigate(value);break;case 'reload':invalidateEditorThumbnails();await loadState();break;case 'reset-stream':if(await loadState())notify('Ungespeicherte Änderungen zurückgesetzt');break;case 'logout':await api('logout','POST');showAuth(false);break;case 'add-camera':cameraDialog();break;case 'delete-camera':confirmCameraDelete(value);break;case 'edit-camera':cameraDialog(camList().find(c=>c.id===value));break;case 'discover':await discoverCameras();break;case 'manage-credentials':credentialManager();break;case 'camera-details':await getCameraDetails(value);break;case 'open-editor':model.cameraId=value;model.viewId=views().find(v=>v.cameraId===value)?.id||'';await navigate('editor');break;case 'new-stream':await makeView();break;case 'select-view':model.viewId=value;await navigate('editor');break;case 'edit-stream':model.viewId=value;model.cameraId=views().find(s=>s.id===value)?.cameraId||'';await navigate('editor');break;case 'refresh-preview':refreshImages();break;case 'copy-url':await navigator.clipboard.writeText(streamAddress(value));notify('RTSP-Adresse kopiert');break;case 'refresh-thumbnails':invalidateEditorThumbnails();notify('Standbilder werden erneuert');break;case 'save-stream':if(await saveEditorDraft())await loadState();break;case 'delete-stream':confirmStreamDelete(value);break;case 'start-pipeline':await api('pipeline/start','POST',{cameraId:model.cameraId});await loadState();notify('Pipeline gestartet');break;case 'stop-pipeline':await api('pipeline/stop','POST',{cameraId:model.cameraId});await loadState();notify('Pipeline angehalten');break;case 'install':askConfirm('Die Komponente '+value+' aus offizieller Quelle herunterladen, überprüfen und installieren?',async()=>{const v=await api('components','POST',{component:value});notify(v.message);await loadState()});break;case 'diagnosis-tab':showDiagnosisTab(value,true);break;case 'restart-media':promptMediaRestart();break;case 'restart-ffmpeg':promptFFmpegRestart();break;case 'save-network':{const networkRtsp=document.getElementById('lanRtsp').checked;const saved=await api('config/network','POST',{networkRtsp,networkOnvif:networkRtsp&&document.getElementById('lanOnvif').checked,networkUi:false});await loadState();if(saved.restartRequired)promptServiceRestart();else notify('Netzwerk gespeichert.');break;}case 'save-log-levels':await saveLogLevels();break;case 'logs':await refreshDiagnosis(true);break;}}catch(e){notify(e.message,true)}});
document.addEventListener('change',async e=>{
 if(e.target.id==='logLevel'){
   const changed=e.target.value!==e.target.dataset.saved;
   if(changed)e.target.dataset.changed='true';else delete e.target.dataset.changed;
   document.querySelector('[data-action="save-log-levels"]').disabled=!changed;
 }
 if(e.target.id==='logCamera'){
   model.diagnosisCameraId=e.target.value;
   updateDiagnosis();
 }
 if(e.target.id==='discoveryCredentialId')model.discoveryCredentialId=e.target.value;if(e.target.id==='cameraSelect'){model.cameraId=e.target.value;model.viewId=views().find(v=>v.cameraId===model.cameraId)?.id||'';await navigate('editor')}});
document.addEventListener('input',e=>{if(e.target.id==='discoveryNetwork')model.discoveryNetwork=e.target.value;if(e.target.id==='discoveryPorts')model.discoveryPorts=e.target.value;if(e.target.matches('[data-slider]')){let key=e.target.dataset.slider;const out=document.getElementById('out-'+key);if(out)out.textContent=(key==='zoom'&&Number(e.target.value)>0?'+':'')+e.target.value+(key==='zoom'?'':'°');markEditorChanged();updateOverlay()}});
document.addEventListener('pointerdown',e=>{if(e.target.closest('#overlay')&&selectedView()){model.overlayDrag=true;document.getElementById('overlay').setPointerCapture(e.pointerId);setCoords(e)}});
document.addEventListener('pointermove',e=>{if(model.overlayDrag)setCoords(e)});document.addEventListener('pointerup',()=>model.overlayDrag=false);

init();




