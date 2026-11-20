'use strict';

const discoveryMethods=[['rtsp','RTSP'],['onvif','ONVIF'],['vendor','Herstellersuche']];
const streamOperations=new Map();
const operationLabels={start:'Startet …',stop:'Stoppt …',restart:'Startet neu …',enabled:'Speichert …'};
const streamStateLabels={online:'Online',offline:'Offline',unhealthy:'Unhealthy'};

function trashButton(action,id,label){
  return `<button class="btn sm danger icon-button" data-action="${action}" data-value="${esc(id)}" aria-label="${label}" title="${label}"><svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 7h16M9 7V4h6v3m3 0-1 13H7L6 7m4 4v5m4-5v5"/></svg></button>`;
}

function cameraForDiscovery(device){
  return camList().find(camera=>{
    try{return new URL(camera.url).hostname===device.address}
    catch{return false}
  });
}

function networkSearchPanel(){
  const opened=!!model.discoveryExpanded;
  const methods=model.discoveryMethods||discoveryMethods.map(([method])=>method);
  const disabled=model.discoveryBusy?'disabled':'';
  const range=model.discoveryNetwork||(model.discoveryNetworks||[]).map(network=>network.cidr).join(', ');
  return `<section class="panel sectiongap">
    <div class="panelhead"><div><h2><button class="section-toggle" type="button" data-operations="toggle-search" aria-expanded="${opened}" aria-controls="discoverySettings"><span aria-hidden="true">${opened?'▾':'▸'}</span> Netzwerksuche</button></h2>
    <small id="currentDiscoveryRange" class="muted">${esc(range)}</small></div>
    ${model.discoveryBusy?'<button class="btn danger" data-operations="cancel-search">Abbrechen</button>':'<button class="btn primary" data-action="discover">Netzwerk durchsuchen</button>'}</div>
    <div class="panelbody" id="discoverySettings" ${opened?'':'hidden'}>
      <fieldset class="search-methods"><legend>Suchverfahren</legend>${discoveryMethods.map(([method,label])=>`<label><input type="checkbox" data-discovery-method value="${method}" ${methods.includes(method)?'checked':''} ${disabled}>${label}</label>`).join('')}</fieldset>
      <div class="formgrid sectiongap">
        <div class="field"><label for="discoveryNetwork">Netzwerkbereich</label><input id="discoveryNetwork" value="${esc(range)}" placeholder="192.168.1.0/24" ${disabled}><small>Einzelne IPs oder Netzbereiche, durch Kommas getrennt.</small>
        <small>${esc((model.discoveryNetworks||[]).map(network=>network.interface+' · '+network.cidr).join(' | '))}</small></div>
        <div class="field"><label for="discoveryPorts">HTTP-Ports der Herstellersuche</label><input id="discoveryPorts" value="${esc(model.discoveryPorts)}" placeholder="80,443" ${disabled}><small>Port 443 verwendet HTTPS.</small></div>
      </div>
      <div class="toolbar search-access-bar sectiongap">
        <div class="field"><label for="discoveryCredentialId">Zugang für diese Suche</label><select id="discoveryCredentialId" ${disabled}><option value="">Ohne Zugang</option>${(model.discoveryCredentials?.profiles||[]).map(p=>`<option value="${esc(p.id)}" ${p.id===model.discoveryCredentialId?'selected':''}>${esc(p.description)} · ${esc(p.username)}</option>`).join('')}</select></div>
        <button class="btn" data-action="manage-credentials">Kamerazugänge verwalten</button>
      </div>
    </div>
    <div class="panelbody discovery-results-panel" ${model.discoveryReport||model.discoveryBusy?'':'hidden'}>
      <div id="discoveryProgress" class="search-progress" role="status" ${model.discoveryBusy?'':'hidden'}>Suche wird vorbereitet …</div>
      ${model.discoveryReport?discoveryFilters():''}<div id="discoveryResults">${discoveryResults()}</div>
    </div>
  </section>`;
}

function discoveryFilters(){
  const filters=model.discoveryFilters||{};
  const manufacturers=[...new Set(model.discover.map(d=>d.details?.manufacturer||d.manufacturer).filter(Boolean))].sort();
  return `<div class="discovery-filters">
    <label>Hersteller<select id="filterManufacturer"><option value="">Alle</option>${manufacturers.map(name=>`<option ${name===filters.manufacturer?'selected':''}>${esc(name)}</option>`).join('')}</select></label>
    <label>IP-Adresse<input type="search" id="filterDiscoveryIP" value="${esc(filters.ip||'')}" placeholder="192.168. …"></label>
    <label>Erkennungsart<select id="filterDiscoveryMethod"><option value="">Alle</option>${discoveryMethods.map(([method,label])=>`<option value="${method}" ${method===filters.method?'selected':''}>${label}</option>`).join('')}</select></label>
    <label class="stream-switch"><input type="checkbox" id="filterDiscoveryNew" ${filters.onlyNew?'checked':''}>Nur neue Geräte</label>
  </div>`;
}

function discoveryMatchesFilters(d){
  const f=model.discoveryFilters||{};
  const method=(d.method||'').toLowerCase();
  return (!f.manufacturer||f.manufacturer===(d.details?.manufacturer||d.manufacturer))&&
    (!f.ip||d.address.includes(f.ip))&&
    (!f.method||(f.method==='vendor'?/http|sadp|vapix/.test(method):method.includes(f.method)))&&
    (!f.onlyNew||!cameraForDiscovery(d));
}

function streamActions(view){
  const work=streamOperations.get(view.cameraId);
  const status=model.health?.get(view.id);
  const disabled=work?'disabled':'';
  return `<div class="stream-controls">
    <button class="btn sm" data-stream-control="start" data-view="${esc(view.id)}" ${disabled||(!view.enabled?'disabled':'')}>Starten</button>
    <button class="btn sm" data-stream-control="stop" data-view="${esc(view.id)}" ${disabled}>Stoppen</button>
    <button class="btn sm" data-stream-control="restart" data-view="${esc(view.id)}" ${disabled||(!view.enabled?'disabled':'')}>Neu starten</button>
    <small role="status" data-operation-status="${esc(view.cameraId)}">${esc(work||'')}</small>
    <button class="btn sm" data-operations="stream-diagnostics" data-view="${esc(view.id)}" ${status?.state==='unhealthy'?'':'hidden'} data-stream-diagnostic>Diagnose</button>
  </div>`;
}

function editorOutput(view){
  return `<label class="stream-switch"><input type="checkbox" id="go1984Compatibility" ${view.go1984Compatibility?'checked':''}>go1984-Kompatibilität</label>
    <details class="sectiongap"><summary>Wann ist diese Option sinnvoll?</summary><p>Bei Bildfehlern in go1984 codiert Spherelay jedes H.264-Bild als einen Slice. Dafür verwendet diese Ansicht einen Encoder-Thread. Das kann bei großen Ansichten die verfügbare Bildrate begrenzen.</p></details>
    <div class="field sectiongap"><label>RTSP-Adresse</label><div class="urlcode">${esc(streamAddress(view.id))}</div></div>
    <div class="toolbar sectiongap">${streamActions(view)}<button class="btn sm" data-action="copy-url" data-value="${esc(view.id)}">Adresse kopieren</button>
    ${trashButton('delete-stream',view.id,'Ansicht löschen')}</div>${pendingPipelineNotice(view.cameraId)}`;
}

function pipelineImpact(cameraId){
  const camera=camList().find(item=>item.id===cameraId);
  const count=views().filter(v=>v.cameraId===cameraId&&v.enabled&&!v.paused).length;
  return `Kamera ${camera?.name||''} · ${count} aktive ${count===1?'Ansicht':'Ansichten'}. Laufende Streams dieser Kamera werden kurz unterbrochen.`;
}

function pendingPipelineNotice(cameraId){
  if(!model.state.cameraRestartPending?.[cameraId])return '';
  return `<div class="pending-change"><span>Neustart ausstehend</span><button class="btn sm" data-operations="restart-camera" data-camera="${esc(cameraId)}">Jetzt neu starten</button></div>`;
}

function promptCameraRestart(cameraId){
  const dialog=showDialog('Kamera-Pipeline neu starten?',`<p>${esc(pipelineImpact(cameraId))}</p>`,'Jetzt neu starten',async()=>{
    await api('pipeline/restart','POST',{cameraId});
    notify('Kamera-Pipeline neu gestartet.');
  });
  dialog.querySelector('#dialogCancel').textContent='Später';
}

function promptServiceRestart(){
  const dialog=showDialog('Spherelay neu starten?', '<p>Der Windows-Dienst und seine Streams werden kurz unterbrochen. Bestätige anschließend die Windows-Abfrage.</p><a href="spherelay://service/restart" class="btn primary sectiongap">Jetzt neu starten</a>','Später',async()=>{});
  dialog.querySelector('#dialogCancel').hidden=true;
}

async function runStreamAction(view,action,enabled){
  if(streamOperations.has(view.cameraId))return;
  streamOperations.set(view.cameraId,operationLabels[action]);
  document.querySelectorAll('[data-stream-control], [data-stream-enabled]').forEach(element=>{
    if(views().find(item=>item.id===element.dataset.view)?.cameraId===view.cameraId)element.disabled=true;
  });
  for(const display of document.querySelectorAll('[data-operation-status]')){
    if(display.dataset.operationStatus===view.cameraId)display.textContent=operationLabels[action];
  }
  try{
    const result=await api('streams/control','POST',{id:view.id,action,enabled:!!enabled});
    return result.restartRequired?()=>promptCameraRestart(view.cameraId):null;
  }finally{
    streamOperations.delete(view.cameraId);
  }
}

async function chooseStreamAction(view,action){
  if(!view||streamOperations.has(view.cameraId))return;
  if(!await confirmEditorLeave())return;
  const perform=async()=>{try{return await runStreamAction(view,action)}finally{await loadState()}};
  const running=model.state.processes?.[view.cameraId]?.running;
  const others=views().some(v=>v.id!==view.id&&v.cameraId===view.cameraId&&v.enabled&&!v.paused);
  if(running&&others){
    showDialog('Streamaktion bestätigen',`<p>${esc(pipelineImpact(view.cameraId))}</p>`,'Ausführen',perform);
  }else await perform();
}

function confirmStreamDelete(id){
  const view=views().find(item=>item.id===id);
  if(!view)return;
  showDialog('Ansicht löschen',`<p>„${esc(view.name)}“ dauerhaft entfernen?</p>`,'Ansicht löschen',async()=>{
    const result=await api('streams/'+encodeURIComponent(id),'DELETE');
    return result.restartRequired?()=>promptCameraRestart(result.cameraId):null;
  });
}

async function openStreamDiagnostics(viewId){
  model.diagnosticStream=viewId;
  model.systemTab='diagnostics';
  model.diagnosisTab='events';
  await navigate('system');
}

function backupPanel(){
  return `<section class="panel sectiongap"><div class="panelhead"><h2>Konfiguration</h2></div><div class="panelbody">
    <p>Sichere Kameras, Ansichten und Einstellungen. Kamerapasswörter und URL-Parameter werden ausgelassen.</p>
    <div class="toolbar sectiongap"><a class="btn" href="/api/config/backup" download>Konfiguration sichern</a>
    <button class="btn" data-operations="restore">Wiederherstellen</button><input id="restoreConfiguration" type="file" accept=".json,application/json" hidden></div>
  </div></section>`;
}

async function prepareConfigurationRestore(file){
  if(!file)return;
  if(file.size>1048576)throw Error('Die Konfigurationsdatei ist zu groß.');
  const backup=JSON.parse(await file.text());
  if(backup.format!==1||!Array.isArray(backup.cameras)||!Array.isArray(backup.streams))throw Error('Ungültige Spherelay-Sicherung.');
  showDialog('Konfiguration wiederherstellen',`<p>Ersetzt die gespeicherte Konfiguration durch ${backup.cameras.length} Kameras und ${backup.streams.length} Ansichten sowie die Netzwerk- und Protokolleinstellungen.</p><p>Nur passende vorhandene Kamerazugänge bleiben nutzbar. Fehlende Zugangsdaten musst du neu eingeben.</p>`,'Wiederherstellen',async()=>{
    const result=await api('config/restore','POST',backup);
    notify('Konfiguration gespeichert. '+result.missingCredentials+' Kamerazugänge neu einrichten.');
    return promptServiceRestart;
  });
}

document.addEventListener('click',async event=>{
  const button=event.target.closest('[data-operations], [data-stream-control]');
  if(!button)return;
  try{
    if(button.dataset.streamControl){
      const view=views().find(item=>item.id===button.dataset.view);
      await chooseStreamAction(view,button.dataset.streamControl);
      return;
    }
    switch(button.dataset.operations){
    case 'toggle-search': {
      model.discoveryExpanded=!model.discoveryExpanded;
      button.setAttribute('aria-expanded',String(model.discoveryExpanded));
      button.firstElementChild.textContent=model.discoveryExpanded?'▾':'▸';
      document.getElementById('discoverySettings').hidden=!model.discoveryExpanded;
      break;
    }
    case 'restart-camera': promptCameraRestart(button.dataset.camera);break;
    case 'cancel-search':
      button.disabled=true;
      button.textContent='Wird abgebrochen …';
      await api('discover/progress','POST');
      break;
    case 'restart-service': promptServiceRestart();break;
    case 'stream-diagnostics': await openStreamDiagnostics(button.dataset.view);break;
    case 'restore': document.getElementById('restoreConfiguration').click();break;
    }
  }catch(err){notify(err.message,true)}
});

document.addEventListener('input',event=>{
  if(event.target.id==='discoveryNetwork'){
    document.getElementById('currentDiscoveryRange').textContent=event.target.value;
  }
  if(!['filterManufacturer','filterDiscoveryIP','filterDiscoveryMethod','filterDiscoveryNew'].includes(event.target.id))return;
  model.discoveryFilters={
    manufacturer:document.getElementById('filterManufacturer').value,
    ip:document.getElementById('filterDiscoveryIP').value.trim(),
    method:document.getElementById('filterDiscoveryMethod').value,
    onlyNew:document.getElementById('filterDiscoveryNew').checked
  };
  document.getElementById('discoveryResults').innerHTML=discoveryResults();
});

document.addEventListener('change',async event=>{
  const element=event.target;
  try{
    if(element.matches('[data-discovery-method]')){
      model.discoveryMethods=[...document.querySelectorAll('[data-discovery-method]:checked')].map(input=>input.value);
    }
    if(element.id==='restoreConfiguration'){
      const file=element.files[0];
      element.value='';
      await prepareConfigurationRestore(file);
    }
    if(element.matches('[data-stream-enabled]')){
      const view=views().find(item=>item.id===element.dataset.view);
      if(!view)return;
      try{
        const after=await runStreamAction(view,'enabled',element.checked);
        await loadState();
        after?.();
      }catch(err){element.checked=view.enabled;await loadState();throw err}
    }
  }catch(err){notify(err.message,true)}
});
