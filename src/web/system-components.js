'use strict';
let componentItems=null,componentPollBusy=false,componentPollTimer;
function componentCards(){return ['ffmpeg','mediamtx'].map(name=>{const s=componentItems?.find(x=>x.name===name)||{installed:!!model.state[name],status:'idle',phase:'Status wird geladen …'},busy=['checking','installing'].includes(s.status);return `<section class="panel"><div class="panelhead"><h2>${name==='ffmpeg'?'FFmpeg':'MediaMTX'}</h2><span class="pill ${s.installed?'ok':'warn'}">${s.installed?'Installiert':'Fehlt'}</span></div><div class="panelbody"><p>Installiert: ${esc(s.installedVersion||'–')}<br>Verfügbar: ${esc(s.latestVersion||'Noch nicht geprüft')}</p><div class="component-status" role="status" aria-live="polite">${busy?'<span class="spinner" aria-label="Vorgang läuft"></span>':''}${esc(s.phase||'Bereit')}${s.message?'<br>'+esc(s.message):''}</div>${s.status==='installing'?`<progress class="component-progress" ${s.total>0?`value="${s.received}" max="${s.total}"`:''}></progress>`:''}<div class="toolbar sectiongap"><button class="btn" data-action="component-task" data-value="${name}" data-task="${s.installed?'check':'install'}" ${busy?'disabled':''}>${s.installed?'Auf Updates prüfen':'Installieren'}</button>${s.updateAvailable?`<button class="btn primary" data-action="component-task" data-value="${name}" data-task="install" ${busy?'disabled':''}>Update installieren</button>`:''}</div></div></section>`}).join('')+appUpdateCard()}
async function pollComponents(){if(!appUpdateState&&!appUpdateChecking)checkAppUpdate();clearTimeout(componentPollTimer);if(componentPollBusy)return;componentPollBusy=true;try{componentItems=(await api('components')).components;const el=document.getElementById('componentCards');if(el)el.innerHTML=componentCards()}catch(e){if(model.page==='system')notify(e.message,true)}finally{componentPollBusy=false;if(model.page==='system'||componentItems?.some(s=>['checking','installing'].includes(s.status)))componentPollTimer=setTimeout(pollComponents,1000)}}
document.addEventListener('click',async e=>{const b=e.target.closest('[data-action="component-task"]');if(!b)return;b.disabled=true;try{await api('components','POST',{component:b.dataset.value,action:b.dataset.task});await pollComponents()}catch(err){notify(err.message,true);b.disabled=false}});

let appUpdateState=null, appUpdateChecking=false, appUpdateInstalling=false;

function appUpdateCard(){
  const state=appUpdateState;
  let message='Prüft stabile Releases auf GitHub.';
  if(appUpdateChecking)message='Suche nach Updates …';
  else if(appUpdateInstalling)message='Windows-Adminabfrage bestätigen. Die Streams werden beim Neustart kurz unterbrochen.';
  else if(state?.error)message=state.error;
  else if(state&&!state.latestVersion)message='Auf GitHub ist noch kein stabiles Release veröffentlicht.';
  else if(state?.canInstall)message='Beim Update werden die Streams kurz unterbrochen.';
  else if(state?.updateAvailable)message=state.hasBinary?'Das direkte Update benötigt die installierte Windows-Dienstversion.':'Für dieses Release fehlt die geprüfte Windows-Programmdatei.';
  else if(state)message='Spherelay ist aktuell.';
  const install=state?.canInstall&&!appUpdateInstalling&&!appUpdateChecking?
    `<a class="btn primary" data-app-update="install" href="spherelay://update/${encodeURIComponent(state.latestVersion)}">Update installieren</a>`:'';
  const result=state?.lastUpdate;
  const report=result?`<details class="sectiongap"><summary>Letztes Update · ${esc(result.version)}</summary>
    <p>${result.serviceReady?'Windows-Dienst läuft':'Dienststart fehlgeschlagen'} · ${esc(new Date(result.checkedAt).toLocaleString('de-DE'))}</p>
    ${(result.streams||[]).map(view=>`<div class="update-view"><span>${esc(view.name)} · ${view.online?'Online':'Unhealthy'}</span>
      ${view.online?'':`<button class="btn sm" data-operations="stream-diagnostics" data-view="${esc(view.id)}">Diagnose</button>`}</div>`).join('')||'<p>Vor dem Update wurde keine aktive Ausgabe erfasst.</p>'}</details>`:'';
  return `<section class="panel"><div class="panelhead"><h2>Spherelay</h2><span class="pill">${esc(model.state.version)}</span></div>
    <div class="panelbody"><p>Installiert: ${esc(model.state.version)}<br>Verfügbar: ${esc(state?.latestVersion||'–')}</p>
    <div class="component-status" role="status" aria-live="polite">${esc(message)}</div>
    <div class="toolbar sectiongap"><button class="btn" data-app-update="check" ${appUpdateChecking||appUpdateInstalling?'disabled':''}>Auf Updates prüfen</button>${install}</div>${report}</div></section>`;
}

function redrawAppUpdateCard(){
  const cards=document.getElementById('componentCards');
  if(cards)cards.innerHTML=componentCards();
}

async function checkAppUpdate(){
  if(appUpdateChecking||appUpdateInstalling)return;
  appUpdateChecking=true;
  redrawAppUpdateCard();
  try{
    appUpdateState=await api('update');
    if(!appUpdateState.lastUpdate&&Date.now()-Date.parse(model.state.started)<45000){
      setTimeout(async()=>{
        try{
          const state=await api('update/result');
          if(state.lastUpdate){appUpdateState.lastUpdate=state.lastUpdate;redrawAppUpdateCard()}
        }catch{}
      },18000);
    }
  }
  catch(error){appUpdateState={error:error.message}}
  finally{appUpdateChecking=false;redrawAppUpdateCard()}
}

async function watchAppUpdate(target){
  const deadline=Date.now()+240000;
  while(Date.now()<deadline){
    await new Promise(resolve=>setTimeout(resolve,1500));
    try{
      const info=await api('info');
      if(info.version===target){window.location.reload();return}
    }catch{}
  }
  appUpdateInstalling=false;
  redrawAppUpdateCard();
}

document.addEventListener('click',event=>{
  const action=event.target.closest('[data-app-update]')?.dataset.appUpdate;
  if(action==='check'){checkAppUpdate();return}
  if(action!=='install'||appUpdateInstalling)return;
  appUpdateInstalling=true;
  const target=appUpdateState.latestVersion;
  // Let the browser dispatch the native link before replacing its element.
  setTimeout(()=>{redrawAppUpdateCard();watchAppUpdate(target)},0);
});
