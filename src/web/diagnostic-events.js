let diagnosticReport, diagnosticVisible=[];
let diagnosticsPaused=false;
const selectedDiagnostics=new Set();
const eventComponents={spherelay:'Spherelay',rtsp:'RTSP',onvif:'ONVIF',mediamtx:'MediaMTX',ffmpeg:'FFmpeg'};
const eventLevels={debug:'Debug',info:'Info',warn:'Warnung',error:'Fehler',critical:'Kritisch'};
function diagnosticOptions(values){
  return '<option value="">Alle</option>'+Object.entries(values).map(([value,label])=>'<option value="'+esc(value)+'">'+esc(label)+'</option>').join('');
}
function diagnosticsEventsPanel(){
  return `<div class="diagnostic-filters" id="diagnosticFilters">
    <label>Suche<input id="diagSearch" type="search" placeholder="Meldung, Session oder Verbindung"></label>
    <label>Ab<input id="diagFrom" type="datetime-local"></label>
    <label>Bis<input id="diagTo" type="datetime-local"></label>
    <label>Komponente<select id="diagComponent">${diagnosticOptions(eventComponents)}</select></label>
    <label>Kamera<select id="diagCamera">${diagnosticOptions(Object.fromEntries(camList().map(c=>[c.id,c.name])))}</select></label>
    <label>Ansicht<select id="diagStream">${diagnosticOptions(Object.fromEntries(views().map(v=>[v.id,v.name]))).replace(`value="${esc(model.diagnosticStream||'')}"`,`value="${esc(model.diagnosticStream||'')}" selected`)}</select></label>
    <label>Stufe<select id="diagLevel">${diagnosticOptions(eventLevels)}</select></label>
    <label>Verbindung<select id="diagStatus">${diagnosticOptions({active:'Aktiv',closed:'Geschlossen',error:'Fehler'})}</select></label>
  </div>
  <div class="toolbar sectiongap" id="diagnosticActions">
    <button class="btn sm" data-diag-action="pause">${diagnosticsPaused?'Live fortsetzen':'Live anhalten'}</button>
    <button class="btn sm" data-diag-action="copy-selected">Markierte kopieren</button>
    <button class="btn sm" data-diag-action="copy-visible">Anzeige kopieren</button>
    <button class="btn sm" data-diag-action="copy-filtered">Alle Treffer kopieren</button>
    <button class="btn sm" data-diag-action="export">Bericht herunterladen</button>
    <span id="diagnosticCount" aria-live="polite"></span>
  </div>
  <div id="diagnosticRows" class="diagnostic-rows sectiongap" tabindex="0"></div>`;
}
function diagnosticLine(event){
  return [event.time,event.lastTime&&('zuletzt '+event.lastTime),event.count&&('×'+event.count),eventComponents[event.component],eventLevels[event.level],event.camera,event.stream,event.connection,event.status,event.message].filter(Boolean).join(' | ');
}
function renderDiagnostics(){
  const rows=document.getElementById('diagnosticRows');
  if(!rows||!diagnosticReport)return;
  const filter=id=>document.getElementById(id).value;
  const camera=filter('diagCamera'), stream=filter('diagStream');
  const component=filter('diagComponent'), level=filter('diagLevel'), status=filter('diagStatus');
  const from=Date.parse(filter('diagFrom')), to=Date.parse(filter('diagTo'));
  const search=filter('diagSearch').trim().toLowerCase();
  const camerasByStream=new Map(diagnosticReport.streams.map(view=>[view.id,view.camera]));
  diagnosticVisible=diagnosticReport.events.filter(event=>
    (!component||event.component===component)&&(!camera||(event.camera||camerasByStream.get(event.stream))===camera)&&
    (!stream||event.stream===stream||(!event.stream&&event.camera&&event.camera===camerasByStream.get(stream)))&&(!level||event.level===level)&&(!status||event.status===status)&&
    (Number.isNaN(from)||Date.parse(event.lastTime||event.time)>=from)&&(Number.isNaN(to)||Date.parse(event.time)<to+60000)&&
    (!search||diagnosticLine(event).toLowerCase().includes(search)));
  const atEnd=rows.scrollHeight-rows.scrollTop-rows.clientHeight<8;
  const top=rows.scrollTop;
  const events=diagnosticVisible.slice(-300);
  const key=events.map(event=>event.id+':'+(event.lastTime||event.time)).join(',');
  if(rows.dataset.key!==key){
    rows.dataset.key=key;
    rows.innerHTML=events.map(event=>`<div class="diagnostic-row severity-${esc(event.level)}">
      <input type="checkbox" data-diag-id="${event.id}" aria-label="Meldung markieren" ${selectedDiagnostics.has(event.id)?'checked':''}>
      <div><strong>${esc(new Date(event.time).toLocaleString('de-DE',{year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit',fractionalSecondDigits:3,timeZoneName:'short'}))} · ${esc(eventComponents[event.component]||event.component)} · ${esc(eventLevels[event.level]||event.level)}${event.count?` · ${event.count}× · zuletzt ${esc(new Date(event.lastTime).toLocaleString('de-DE',{timeZoneName:'short'}))}`:''}</strong><span>${esc(event.message)}</span></div>
    </div>`).join('')||'Keine passenden Ereignisse.';
    rows.scrollTop=atEnd?rows.scrollHeight:top;
  }
  document.getElementById('diagnosticCount').textContent=diagnosticVisible.length+' Treffer'+(diagnosticVisible.length>300?' · letzte 300 angezeigt':'')+' · '+selectedDiagnostics.size+' markiert';
}
async function refreshDiagnosticEvents(force=false){
  if(diagnosticsPaused&&!force)return;
  const rows=document.getElementById('diagnosticRows');
  if(!rows)return;
  const report=await api('diagnostics');
  if(!rows.isConnected)return;
  if(diagnosticReport&&report.serviceStarted!==diagnosticReport.serviceStarted)selectedDiagnostics.clear();
  diagnosticReport=report;
  const ids=new Set(report.events.map(event=>event.id));
  for(const id of selectedDiagnostics)if(!ids.has(id))selectedDiagnostics.delete(id);
  renderDiagnostics();
}
document.addEventListener('input',event=>{
  if(event.target.id==='diagStream')model.diagnosticStream=event.target.value;
  if(event.target.closest('#diagnosticFilters'))renderDiagnostics();
  if(event.target.dataset.diagId){
    const id=Number(event.target.dataset.diagId);
    if(event.target.checked)selectedDiagnostics.add(id);else selectedDiagnostics.delete(id);
    renderDiagnostics();
  }
});
document.addEventListener('click',async event=>{
  const button=event.target.closest('[data-diag-action]');
  if(!button)return;
  try{
    const action=button.dataset.diagAction;
    if(action==='pause'){
      diagnosticsPaused=!diagnosticsPaused;
      button.textContent=diagnosticsPaused?'Live fortsetzen':'Live anhalten';
      if(!diagnosticsPaused)await refreshDiagnosticEvents(true);
      return;
    }
    if(!diagnosticReport)return;
    if(action==='export'){
      const url=URL.createObjectURL(new Blob([JSON.stringify(diagnosticReport,null,2)],{type:'application/json'}));
      const a=document.createElement('a');
      a.href=url;a.download='spherelay-diagnose.json';a.click();
      setTimeout(()=>URL.revokeObjectURL(url),1000);
      return;
    }
    const events=action==='copy-visible'?diagnosticVisible.slice(-300):
      action==='copy-filtered'?diagnosticVisible:
      diagnosticReport.events.filter(item=>selectedDiagnostics.has(item.id));
    if(!events.length){notify('Keine Meldungen ausgewählt');return;}
    await navigator.clipboard.writeText(events.map(diagnosticLine).join('\n'));
    notify(events.length+' Meldung(en) kopiert');
  }catch(error){notify(error.message,true);}
});
