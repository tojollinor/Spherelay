'use strict';
let editorPreview={controller:null,bitmap:null,renderer:null,lastFrame:0,timer:null,generation:0,circle:[.5,.5,1]};

function previewDraft(view){
 if(view.id!==model.viewId)return constrainView(view);
 const resolution=document.getElementById('viewResolution')?.value.split('x').map(Number)||[view.width,view.height];
 return constrainView({...view,name:document.getElementById("viewName")?.value??view.name,roll:Number(document.getElementById("slide-roll")?.value??view.roll??0),osd:readOSD(),mirrorHorizontal:document.getElementById("mirrorHorizontal")?.checked??view.mirrorHorizontal??false,mirrorVertical:document.getElementById("mirrorVertical")?.checked??view.mirrorVertical??false,width:resolution[0],height:resolution[1],yaw:Number(document.getElementById('slide-yaw')?.value??view.yaw),pitch:Number(document.getElementById('slide-pitch')?.value??view.pitch),fov:Number(document.getElementById('slide-fov')?.value??view.fov)});
}

function viewRay(x,y,view){
 const radians=Math.PI/180;const yaw=view.yaw*radians,pitch=view.pitch*radians;
 const a=x*Math.tan(view.fov*radians/2),b=y*Math.tan(verticalFOV(view)*radians/2),r=(view.roll||0)*radians;const vx=Math.cos(r)*a-Math.sin(r)*b,vy=Math.sin(r)*a+Math.cos(r)*b;
 const py=Math.cos(pitch)*vy-Math.sin(pitch),pz=Math.sin(pitch)*vy+Math.cos(pitch);
 return [Math.cos(yaw)*vx+Math.sin(yaw)*pz,py,-Math.sin(yaw)*vx+Math.cos(yaw)*pz];
}

function rayToSource(ray,projection){
 const [x,y,z]=ray,length=Math.hypot(x,y,z);
 if(projection==='equirect')return [Math.atan2(x,z)/(2*Math.PI)+.5,Math.asin(Math.max(-1,Math.min(1,y/length)))/Math.PI+.5];
 if(z<0)return null;const radial=Math.hypot(x,y),angle=Math.atan2(radial,z)/Math.PI;
 return [radial>1e-9?.5+x/radial*angle:.5,radial>1e-9?.5+y/radial*angle:.5];
}

function makePreviewRenderer(){
 const canvas=document.createElement('canvas');const gl=canvas.getContext('webgl',{alpha:false,preserveDrawingBuffer:true});
 if(!gl)throw Error('Dieser Browser bietet keine Grafikbeschleunigung für die Ausschnittvorschau.');
 const compile=(type,source)=>{const shader=gl.createShader(type);gl.shaderSource(shader,source);gl.compileShader(shader);if(!gl.getShaderParameter(shader,gl.COMPILE_STATUS))throw Error('Vorschaugrafik konnte nicht gestartet werden');return shader};
 const vertex=compile(gl.VERTEX_SHADER,'attribute vec2 point;varying vec2 uv;void main(){uv=(point+1.0)*0.5;gl_Position=vec4(point,0.0,1.0);}');
 const fragment=compile(gl.FRAGMENT_SHADER,`precision highp float;varying vec2 uv;uniform sampler2D image;uniform vec4 pose;uniform float panoramic;uniform float roll;uniform vec3 circle;
 void main(){
  vec2 p=vec2(2.0*uv.x-1.0,1.0-2.0*uv.y);
  vec3 ray=vec3(p.x*tan(pose.z*0.5),p.y*tan(pose.w*0.5),1.0);
  ray.xy=mat2(cos(roll),sin(roll),-sin(roll),cos(roll))*ray.xy;float cy=cos(pose.x),sy=sin(pose.x),cp=cos(pose.y),sp=sin(pose.y);
  float y=cp*ray.y-sp*ray.z,z=sp*ray.y+cp*ray.z;
  ray=vec3(cy*ray.x+sy*z,y,-sy*ray.x+cy*z);
  vec2 sampleUV;
  if(panoramic>0.5){sampleUV=vec2(atan(ray.x,ray.z)/6.28318530718+0.5,asin(clamp(normalize(ray).y,-1.0,1.0))/3.14159265359+0.5);}
  else{float r=length(ray.xy);float a=atan(r,ray.z)/3.14159265359;sampleUV=vec2(0.5)+(r>0.000001?ray.xy/r*a:vec2(0.0));if(ray.z<0.0){gl_FragColor=vec4(0.025,0.055,0.075,1.0);return;}}
  if(panoramic<0.5)sampleUV=circle.xy+(sampleUV-vec2(0.5))*circle.z;gl_FragColor=texture2D(image,clamp(sampleUV,vec2(0.0),vec2(1.0)));
 }`);
 const program=gl.createProgram();gl.attachShader(program,vertex);gl.attachShader(program,fragment);gl.linkProgram(program);if(!gl.getProgramParameter(program,gl.LINK_STATUS))throw Error('Vorschaugrafik nicht verfügbar');gl.useProgram(program);
 const buffer=gl.createBuffer();gl.bindBuffer(gl.ARRAY_BUFFER,buffer);gl.bufferData(gl.ARRAY_BUFFER,new Float32Array([-1,-1,1,-1,-1,1,-1,1,1,-1,1,1]),gl.STATIC_DRAW);
 const point=gl.getAttribLocation(program,'point');gl.enableVertexAttribArray(point);gl.vertexAttribPointer(point,2,gl.FLOAT,false,0,0);
 const texture=gl.createTexture();gl.bindTexture(gl.TEXTURE_2D,texture);gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_MIN_FILTER,gl.LINEAR);gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_MAG_FILTER,gl.LINEAR);gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_WRAP_S,gl.CLAMP_TO_EDGE);gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_WRAP_T,gl.CLAMP_TO_EDGE);
 return {canvas,gl,pose:gl.getUniformLocation(program,'pose'),panoramic:gl.getUniformLocation(program,'panoramic'),roll:gl.getUniformLocation(program,'roll'),circle:gl.getUniformLocation(program,'circle')};
}

const editorThumbnails=new Map();
function invalidateEditorThumbnails(){editorThumbnails.clear();for(const canvas of document.querySelectorAll('[data-thumb]'))delete canvas.dataset.snapshotKey;if(model.page==='editor'&&editorPreview.bitmap)drawEditorViews()}
function paintEditorThumbnail(thumb,cached){thumb.width=cached.image.width;thumb.height=cached.image.height;thumb.getContext('2d').drawImage(cached.image,0,0);thumb.dataset.snapshotKey=cached.key;thumb.dataset.frame=String(cached.frame);thumb.parentElement.querySelector('.thumb-wait')?.setAttribute('hidden','')}
function restoreEditorThumbnails(){for(const saved of camViews()){const cached=editorThumbnails.get(saved.id),thumb=document.getElementById('thumb-'+saved.id);if(thumb&&cached&&cached.key===JSON.stringify(saved))paintEditorThumbnail(thumb,cached)}}
function renderProjectedView(view,target,maxWidth,maxHeight){const {canvas,gl,pose,roll}=editorPreview.renderer,scale=Math.min(1,maxWidth/view.width,maxHeight/view.height),width=Math.max(1,Math.round(view.width*scale)),height=Math.max(1,Math.round(view.height*scale));if(canvas.width!==width||canvas.height!==height){canvas.width=width;canvas.height=height}gl.viewport(0,0,width,height);gl.uniform1f(roll,(view.roll||0)*Math.PI/180);gl.uniform4f(pose,view.yaw*Math.PI/180,view.pitch*Math.PI/180,view.fov*Math.PI/180,verticalFOV(view)*Math.PI/180);gl.drawArrays(gl.TRIANGLES,0,6);if(target.width!==width||target.height!==height){target.width=width;target.height=height}const ctx=target.getContext('2d');ctx.save();ctx.translate(view.mirrorHorizontal?width:0,view.mirrorVertical?height:0);ctx.scale(view.mirrorHorizontal?-1:1,view.mirrorVertical?-1:1);ctx.drawImage(canvas,0,0);ctx.restore();drawOSD(target,view);target.style.display='block';target.dataset.frame=String(editorPreview.lastFrame);if(target.parentElement?.classList.contains('active-preview-image'))target.parentElement.style.setProperty('--view-aspect',view.width+'/'+view.height)}
function drawEditorViews(){if(model.page!=='editor'||!editorPreview.bitmap)return;try{if(!editorPreview.renderer)editorPreview.renderer=makePreviewRenderer();const {gl,panoramic,circle}=editorPreview.renderer;gl.bindTexture(gl.TEXTURE_2D,gl.getParameter(gl.TEXTURE_BINDING_2D));gl.texImage2D(gl.TEXTURE_2D,0,gl.RGBA,gl.RGBA,gl.UNSIGNED_BYTE,editorPreview.bitmap);gl.uniform1f(panoramic,selectedCam()?.projection==='equirect'?1:0);gl.uniform3fv(circle,editorPreview.circle);
 for(const saved of camViews()){const target=document.getElementById('preview-'+saved.id);if(target){const view=previewDraft(saved);renderProjectedView(view,target,1920,1080);const format=document.getElementById('preview-format-'+view.id);if(format)format.textContent=formatLabel(view);const title=document.getElementById('activeViewTitle');if(title)title.textContent=view.name;const label=document.getElementById('preview-status-'+view.id);if(label)label.textContent='Live · '+new Date(editorPreview.lastFrame).toLocaleTimeString()}
 const thumb=document.getElementById('thumb-'+saved.id);if(!thumb)continue;const key=JSON.stringify(saved);let cached=editorThumbnails.get(saved.id);if(!cached||cached.key!==key){const image=document.createElement('canvas');renderProjectedView(constrainView(saved),image,240,180);cached={key,image,frame:editorPreview.lastFrame};if(editorThumbnails.size>64)editorThumbnails.clear();editorThumbnails.set(saved.id,cached)}if(thumb.dataset.snapshotKey!==cached.key)paintEditorThumbnail(thumb,cached)
 }
 }catch(error){setEditorPreviewStatus(error.message,true)}}

function setEditorPreviewStatus(text,error=false){const el=document.getElementById('editorPreviewStatus');if(el){el.textContent=text;el.classList.toggle('preview-error',error)}}
function stopEditorPreview(){editorPreview.generation++;editorPreview.controller?.abort();editorPreview.controller=null;clearTimeout(editorPreview.timer);editorPreview.bitmap?.close();editorPreview.bitmap=null;}

async function startEditorPreview(){
 stopEditorPreview();if(model.page!=='editor'||!model.cameraId)return;
 const generation=editorPreview.generation,cameraId=model.cameraId;let after='0';
 const controller=new AbortController();editorPreview.controller=controller;setEditorPreviewStatus('Verbindung zur Kamera wird aufgebaut …');
 while(generation===editorPreview.generation&&!controller.signal.aborted){
  try{
   const response=await fetch('/api/preview/frame?camera='+encodeURIComponent(cameraId)+'&after='+after,{credentials:'same-origin',signal:controller.signal,cache:'no-store'});
   if(!response.ok){let result={};try{result=await response.json()}catch{}throw Error(result.error||'Kameravorschau nicht erreichbar')}
   editorPreview.circle=(response.headers.get('X-Spherelay-Circle')||'.5,.5,1').split(',').slice(0,3).map(Number);const bitmap=await createImageBitmap(await response.blob());if(generation!==editorPreview.generation){bitmap.close();return}
   editorPreview.bitmap?.close();editorPreview.bitmap=bitmap;after=response.headers.get('X-Spherelay-Frame')||'0';editorPreview.lastFrame=Number(response.headers.get('X-Spherelay-Frame-Time'))||Date.now();
   const original=document.getElementById('originalCanvas');if(!original)return;if(original.width!==bitmap.width||original.height!==bitmap.height){original.width=bitmap.width;original.height=bitmap.height}const [cx,cy,r]=editorPreview.circle;if(selectedCam()?.projection==='equirect')original.getContext('2d').drawImage(bitmap,0,0);else original.getContext('2d').drawImage(bitmap,(cx-r/2)*bitmap.width,(cy-r/2)*bitmap.height,r*bitmap.width,r*bitmap.height,0,0,original.width,original.height);original.style.display='block';document.querySelector('.blanklens')?.remove();
   setEditorPreviewStatus('Live-Kamerabild '+bitmap.width+'×'+bitmap.height+' · bis zu 4 Bilder/s · '+new Date(editorPreview.lastFrame).toLocaleTimeString());drawEditorViews();
  }catch(error){if(controller.signal.aborted||generation!==editorPreview.generation)return;setEditorPreviewStatus(error.message,true);await new Promise(resolve=>{editorPreview.timer=setTimeout(resolve,2500)});}
 }
}

function updateLiveOverlay(){
 const element=document.getElementById('overlay');if(!element)return;const panoramic=selectedCam()?.projection==='equirect',height=panoramic?180:360;let markup='';element.setAttribute('viewBox','0 0 360 '+height);
 if(!panoramic)markup='<defs><clipPath id="lensClip"><circle cx="180" cy="180" r="179"/></clipPath></defs><circle cx="180" cy="180" r="179" fill="none" stroke="#cfe7e944" stroke-width="1.5"/>';
 camViews().forEach((saved,index)=>{
  const view=previewDraft(saved);const perimeter=[];for(let i=0;i<17;i++)perimeter.push([-1+i/8,-1]);for(let i=1;i<17;i++)perimeter.push([1,-1+i/8]);for(let i=1;i<17;i++)perimeter.push([1-i/8,1]);for(let i=1;i<17;i++)perimeter.push([-1,1-i/8]);
  const points=perimeter.map(([x,y])=>rayToSource(viewRay(x,y,view),selectedCam()?.projection));let path='',last=null,complete=points.every(Boolean);
  for(const uv of points){if(!uv){last=null;continue}const p=[uv[0]*360,uv[1]*height];if(last&&Math.abs(p[0]-last[0])>180){last=null;complete=false}path+=(last?' L ':' M ')+p.join(' ');last=p}
  if(complete)path+=' Z';markup+='<path '+(panoramic?'':'clip-path="url(#lensClip)" ')+'d="'+path+'" fill="'+(complete?color(index):'none')+'" fill-opacity=".13" stroke="'+color(index)+'" stroke-width="'+(saved.id===model.viewId?2.5:1.3)+'"/>';
  const center=rayToSource(viewRay(0,0,view),selectedCam()?.projection);if(center)markup+='<text x="'+center[0]*360+'" y="'+center[1]*height+'" fill="'+color(index)+'" font-size="14" font-weight="bold" text-anchor="middle">'+(index+1)+'</text>';
 });element.innerHTML=markup;
}

function liveClickAngles(event){
 const box=document.getElementById('overlay').getBoundingClientRect(),u=Math.max(0,Math.min(1,(event.clientX-box.left)/box.width)),v=Math.max(0,Math.min(1,(event.clientY-box.top)/box.height));
 if(selectedCam()?.projection==='equirect')return {yaw:Math.round((u-.5)*360),pitch:Math.round((.5-v)*180)};
 const x=u*2-1,y=v*2-1,r=Math.hypot(x,y),angle=Math.min(1,r)*Math.PI/2,s=r>1e-9?Math.sin(angle)/r:0;
 return {yaw:Math.round(Math.atan2(x*s,Math.cos(angle))*180/Math.PI),pitch:Math.round(-Math.asin(Math.max(-1,Math.min(1,y*s)))*180/Math.PI)};
}

document.addEventListener('click',event=>{if(event.target.closest('[data-action="preview-center"]')){for(const key of ['yaw','pitch']){const el=document.getElementById('slide-'+key);if(el)el.value='0';const output=document.getElementById('out-'+key);if(output)output.textContent='0°'}markEditorChanged();updateLiveOverlay();drawEditorViews()}});
document.addEventListener('input',event=>{if(event.target.matches('[data-slider]')||event.target.id==='viewResolution'){updateLiveOverlay();drawEditorViews()}});
document.addEventListener('pointermove',()=>{if(model.overlayDrag){updateLiveOverlay();drawEditorViews()}});
document.addEventListener('pointerdown',event=>{if(event.target.closest('#overlay'))drawEditorViews()});
window.addEventListener('pagehide',stopEditorPreview);

function constrainView(v){
 v={...v,roll:v.roll||0};if(selectedCam()?.projection==='equirect')return v;v.roll=radialRoll(v);
 const fits=q=>[-1,1].every(x=>[-1,1].every(y=>viewRay(x,y,q)[2]>=.012));if(fits(v))return v;
 const [x,y,z]=viewRay(0,0,v),a=Math.atan2(y,x);let low=0,high=Math.atan2(Math.hypot(x,y),z);
 const at=t=>{const q={...v,yaw:Math.atan2(Math.sin(t)*Math.cos(a),Math.cos(t))*180/Math.PI,pitch:-Math.asin(Math.sin(t)*Math.sin(a))*180/Math.PI};q.roll=radialRoll(q);return q};
 for(let i=0;i<32;i++){let m=(low+high)/2;if(fits(at(m)))low=m;else high=m}return at(low);
}
function orientationControls(v,c){const o=v.osd||{viewName:true,clock:true};return `<div class="settings-row sectiongap">${slider('yaw','Horizont',v.yaw,-180,180,1)}${slider('pitch','Neigung',v.pitch,-90,90,1)}${c.projection==='equirect'?slider('roll','Drehung',v.roll||0,-180,180,1):'<input type="hidden" id="slide-roll" value="'+radialRoll(v)+'"><div class="help">Ausrichtung: automatisch zur Bildmitte</div>'}${slider('orbit','Umlauf',orbitOf(v),-180,180,1)}${slider('distance','Abstand zur Mitte',distanceOf(v),0,89,1)}${slider('zoom','Zoom (− weit / + nah)',fovToZoom(v.fov),-100,100,1)}${slider('fov','Bildwinkel',v.fov,15,160,1)}</div><p>Die Ansicht dreht sich beim Verschieben automatisch um die Fisheye-Mitte. Unten bleibt unten, links und rechts dreht sie mit, oben steht sie um 180° gedreht. Der Bildrand begrenzt das Sichtfeld.</p><div class="toolbar sectiongap"><label class="switch"><input type="checkbox" id="mirrorHorizontal" ${v.mirrorHorizontal?'checked':''}>Horizontal spiegeln</label><label class="switch"><input type="checkbox" id="mirrorVertical" ${v.mirrorVertical?'checked':''}>Vertikal spiegeln</label></div><p>Beide Spiegelungen zusammen drehen das Bild um 180°. Die Texteinblendung bleibt lesbar.</p><div class="toolbar sectiongap">${[['cameraName','Kameraname'],['viewName','Ansichtsname'],['clock','Uhrzeit']].map(([key,label])=>`<label class="switch"><input type="checkbox" id="osd-${key}" ${o[key]?'checked':''}>${label}</label>`).join('')}</div>${osdControls(v)}`}
function orbitOf(v){const [x,y]=viewRay(0,0,v);return Math.round(Math.atan2(y,x)*180/Math.PI)}
function distanceOf(v){const [x,y,z]=viewRay(0,0,v);return Math.round(Math.atan2(Math.hypot(x,y),z)*180/Math.PI)}
function syncOrientation(){const zoom=document.getElementById('slide-zoom'),fov=document.getElementById('slide-fov');if(zoom&&fov){zoom.value=fovToZoom(Number(fov.value));const fovOut=document.getElementById('out-fov');if(fovOut)fovOut.textContent=Math.round(Number(fov.value))+'°';const out=document.getElementById('out-zoom');if(out)out.textContent=(Number(zoom.value)>0?'+':'')+zoom.value}const saved=selectedView();if(!saved)return;const v=previewDraft(saved);for(const key of ['yaw','pitch','roll']){const e=document.getElementById('slide-'+key);if(e)e.value=v[key]||0;const o=document.getElementById('out-'+key);if(o)o.textContent=Math.round(v[key]||0)+'°'}for(const [key,value] of [['orbit',orbitOf(v)],['distance',distanceOf(v)]]){const e=document.getElementById('slide-'+key);if(e)e.value=value;const o=document.getElementById('out-'+key);if(o)o.textContent=value+'°'}}
document.addEventListener('input',e=>{if(e.target.id==='slide-zoom'){document.getElementById('slide-fov').value=zoomToFOV(Number(e.target.value))}if(['slide-orbit','slide-distance'].includes(e.target.id)){const a=Number(document.getElementById('slide-orbit').value)*Math.PI/180,t=Number(document.getElementById('slide-distance').value)*Math.PI/180;document.getElementById('slide-yaw').value=Math.atan2(Math.sin(t)*Math.cos(a),Math.cos(t))*180/Math.PI;document.getElementById('slide-pitch').value=-Math.asin(Math.sin(t)*Math.sin(a))*180/Math.PI}if(e.target.matches('[data-slider],#viewResolution,#viewName,[id^="osd-"],#mirrorHorizontal,#mirrorVertical')){model.changed=true;syncOrientation();updateLiveOverlay();drawEditorViews()}});
document.addEventListener('pointermove',()=>{if(model.overlayDrag)syncOrientation()});
document.addEventListener('click',e=>{const b=e.target.closest('[data-fullscreen]');if(b){e.stopPropagation();document.getElementById(b.dataset.fullscreen)?.requestFullscreen().catch(err=>notify(err.message,true))}});

function radialRoll(v){const d=Math.PI/180,a=v.yaw*d,p=v.pitch*d,x=Math.sin(a)*Math.cos(p),y=-Math.sin(p),z=Math.cos(a)*Math.cos(p),r=Math.hypot(x,y);if(r<1e-8)return 0;const tx=z*x/r,ty=z*y/r,tz=-r;return Math.atan2(-(tx*Math.cos(a)-tz*Math.sin(a)),tx*Math.sin(a)*Math.sin(p)+ty*Math.cos(p)+tz*Math.cos(a)*Math.sin(p))/d}

function verticalFOV(v){return 2*Math.atan(Math.tan(v.fov*Math.PI/360)*v.height/v.width)*180/Math.PI}
function formatLabel(v){const gcd=(a,b)=>b?gcd(b,a%b):a,g=gcd(v.width,v.height);return v.width+'×'+v.height+' · '+v.width/g+':'+v.height/g}


function zoomToFOV(z){const base=Math.tan(40*Math.PI/180),target=Math.tan((z>=0?7.5:80)*Math.PI/180);return 2*Math.atan(base*Math.pow(target/base,Math.abs(z)/100))*180/Math.PI}
function fovToZoom(fov){const base=Math.tan(40*Math.PI/180),narrow=fov<=80,target=Math.tan((narrow?7.5:80)*Math.PI/180);return Math.round((narrow?1:-1)*100*Math.log(Math.tan(fov*Math.PI/360)/base)/Math.log(target/base))}
