'use strict';
const el = id => document.getElementById(id);
const page = document.querySelector('meta[name="page"]')?.content;
const csrf = document.querySelector('meta[name="csrf-token"]')?.content;
const dateText = text => text ? new Date(text).toLocaleString() : '—';
const numeric = (value, suffix, digits=0) => typeof value === 'number' ? value.toFixed(digits) + suffix : 'Not reported';
const humanDuration = seconds => seconds >= 3600 ? (seconds/3600).toFixed(1) + ' h' : seconds >= 60 ? Math.round(seconds/60) + ' min' : seconds + ' sec';
async function getJSON(path) {
 const res = await fetch(path, {cache:'no-store',credentials:'same-origin'});
 if (res.status === 401) { location.assign('/login'); throw new Error('Session expired'); }
 if (!res.ok) throw new Error('Request failed (' + res.status + ')');
 return res.json();
}
function makeTimeline(root,events) {
 root.replaceChildren();
 if (!events.length) {const p=document.createElement('p');p.className='muted';p.textContent='No events recorded in this period.';root.append(p);return;}
 for(const e of events){
  const item=document.createElement('div');item.className='event-row';
  const dot=document.createElement('span');dot.className='dot' + (e.level==='warning'||e.level==='critical'?' warning':'');
  const info=document.createElement('div');const title=document.createElement('div');title.className='event-name';title.textContent=e.type.replaceAll('_',' ');
  const desc=document.createElement('div');desc.className='event-detail';
  const details=e.details||{};desc.textContent=[details.device,details.status,typeof details.battery_percent==='number'?details.battery_percent+'% battery':'',details.reason||''].filter(Boolean).join(' · ');
  info.append(title,desc);const when=document.createElement('time');when.className='event-time';when.textContent=dateText(e.time);item.append(dot,info,when);root.append(item);
 }
}
async function refreshDashboard(){
 try{
  const s=await getJSON('/api/status'); const current=s.current;const badge=el('health');
  badge.className='state '+(s.healthy ? (current?.power==='ON_BATTERY'?'warn':'ok'):'error');
  badge.textContent=s.healthy ? (current.power==='ON_BATTERY'?'On battery':'Online') : 'Connection issue';
  const r=current||s.last_good||{};
  el('battery').textContent=numeric(r.battery_percent,'%');el('runtime').textContent=numeric(r.runtime_minutes,' min',1);
  el('load').textContent=numeric(r.load_percent,'%');el('voltage').textContent=numeric(r.input_volts,' V',1);
  el('upsname').textContent=r.name||'Not reported';el('model').textContent=r.model||'Not reported';
  el('power').textContent=current?.status||'Unavailable';el('output').textContent=numeric(r.output_volts,' V',1);
  el('checked').textContent=dateText(r.observed_at);el('communication').textContent=s.healthy?'Connected':'Failed · '+(s.error||'Unknown reason');
  el('battery-note').textContent=s.healthy?'Live UPS reading':'Showing last known value · stale';
  el('battery-bar').style.width=typeof r.battery_percent==='number'?Math.min(100,Math.max(0,r.battery_percent))+'%':'0%';
  el('battery-bar').className='battery-fill'+(typeof r.battery_percent==='number'&&r.battery_percent<=20?' low':'');
 }catch(err){el('health').textContent='Unable to load status';el('health').className='state error';}
}
async function refreshFirmware(){
 const warning=el('firmware-warning');
 if(!warning)return;
 try {
  const fw=await getJSON('/api/firmware');
  warning.hidden=fw.status!=='outdated';
  if(fw.status==='outdated'){
   el('firmware-message').textContent=fw.message;
   el('firmware-versions').textContent='Installed: '+fw.installed+' · Latest referenced: UPS '+fw.latest+' · Reference reviewed: '+fw.reference_date;
   // The update link is a fixed trusted Schneider Electric URL, not device data.
  }
 } catch(e) {warning.hidden=true;}
}
async function refreshRecent(){try{makeTimeline(el('recent-events'),await getJSON('/api/events?limit=5&days=30'));}catch(e){el('recent-events').textContent='Event history unavailable';}}
async function refreshEvents(){
 try{
  const days=el('range').value;el('export-events').href='/api/events.csv?days='+days+'&limit=500';
  const [events,report]=await Promise.all([getJSON('/api/events?limit=200&days='+days),getJSON('/api/report?days='+days)]);
  el('outages').textContent=report.outages;
  el('downtime').textContent=humanDuration(report.observed_outage_seconds);
  el('longest').textContent=humanDuration(report.longest_observed_outage_seconds);
  el('failures').textContent=report.communication_failures;
  makeTimeline(el('event-list'),events);
 }catch(e){el('event-list').textContent='Unable to read reports';}
}
async function startSettings(){
 try{
  const v=await getJSON('/api/settings');el('poll').value=v.poll_interval_seconds;el('low').value=v.low_battery_percent;el('retention').value=v.retention_days;
 }catch(e){el('settings-feedback').textContent='Unable to load settings';}
 el('settings-form').addEventListener('submit',async event=>{
  event.preventDefault();const feedback=el('settings-feedback');feedback.textContent='Saving…';
  const value={poll_interval_seconds:Number(el('poll').value),low_battery_percent:Number(el('low').value),retention_days:Number(el('retention').value)};
  try{
   const res=await fetch('/api/settings',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify(value)});
   if (!res.ok) throw new Error(await res.text());
   feedback.textContent='Settings saved and applied.';
  }catch(e){feedback.textContent='Could not save: '+e.message;}
 });
}
async function refreshPowerState(){
 try {const v=await getJSON('/api/power/state');
 const text=v.enabled ? (v.active ? 'Active: '+v.phase.replaceAll('_',' ') : (v.armed?'Armed · waiting for outage':'Enabled · not yet armed')) : 'Disabled · remote actions off';
 if(el('automation-state'))el('automation-state').textContent=text;
 if(el('power-state')){el('power-state').textContent=text;el('power-state').className='state '+(v.active?'warn':v.enabled?'ok':'');}
 if(el('power-summary')){const actions=Object.entries(v.actions||{}).map(([id,a])=>{const n=a.shutdown_attempts||(a.shutdown_attempted?1:0);return id+': '+(a.shutdown_confirmed?'confirmed off':a.shutdown_gave_up?'gave up':a.shutdown_accepted?'shutdown sent':n>0?'retrying ('+n+'/5)':'shutdown pending')+(a.wake_sent?', wake sent':a.wake_attempted?', wake attempted':'');});
 el('power-summary').textContent=text+(actions.length?' · '+actions.join(' | '):'');}
 }catch(e){if(el('automation-state'))el('automation-state').textContent='Automation status unavailable';}
}
function powerField(parent,label,key,value,type='text'){
 const wrapper=document.createElement('label');wrapper.className=type==='checkbox'?'checkline':'device-field';
 const input=document.createElement('input');input.dataset.key=key;input.type=type;
 if(type==='checkbox')input.checked=!!value;else input.value=value??'';
 if(type==='number')input.step=key==='priority'||key.includes('port')?'1':'any';
 if(key==='id'||key==='name'||key==='host'||key==='ssh_user')input.required=true;
 const name=document.createElement('span');name.textContent=label;
 wrapper.append(name,input);parent.append(wrapper);
 return input;
}
function addDevice(data={}){
 const root=el('device-list');const section=document.createElement('section');section.className='device';
 const top=document.createElement('div');top.className='device-top';const title=document.createElement('strong');title.textContent=data.name||'New device';
 const remove=document.createElement('button');remove.type='button';remove.textContent='Remove';remove.className='secondary';remove.addEventListener('click',()=>section.remove());top.append(title,remove);section.append(top);
 const fields=document.createElement('div');fields.className='device-fields';section.append(fields);
 const spec=[['Device ID','id','text',data.id||'node-'+Math.random().toString(36).slice(2,8)],['Friendly name','name','text',data.name||''],['IP or hostname','host','text',data.host||''],['SSH user','ssh_user','text',data.ssh_user||'root'],['SSH port','ssh_port','number',data.ssh_port??22],['Shutdown priority','priority','number',data.priority??0],['MAC address','mac','text',data.mac||''],['Wake destination IP','wake_address','text',data.wake_address||'255.255.255.255'],['Wake UDP port','wake_port','number',data.wake_port??9],['Use sudo -n','use_sudo','checkbox',data.use_sudo||false],['Allow automatic shutdown','shutdown_enabled','checkbox',data.shutdown_enabled||false],['Allow wake after shutdown','wake_enabled','checkbox',data.wake_enabled||false]];
 for(const [label,key,type,val] of spec)powerField(fields,label,key,val,type);
 root.append(section);
}
function collectDevices(){return Array.from(el('device-list').querySelectorAll('.device')).map(section=>{
 const d={};for(const input of section.querySelectorAll('[data-key]'))d[input.dataset.key]=input.type==='checkbox'?input.checked:input.type==='number'?Number(input.value):input.value.trim();return d;
});}
async function startPower(){
 const feedback=el('power-feedback');
 try{const v=await getJSON('/api/power');
 el('power-enabled').checked=v.enabled;el('shutdown-percent').value=v.shutdown_battery_percent;el('shutdown-runtime').value=v.shutdown_runtime_minutes;
 el('outage-delay').value=v.on_battery_delay_seconds;el('recovery-percent').value=v.recovery_battery_percent;el('recovery-delay').value=v.recovery_stable_seconds;
 for(const d of v.devices||[])addDevice(d);
 }catch(e){feedback.textContent='Could not load power configuration.';return;}
 el('add-device').addEventListener('click',()=>addDevice());
 el('power-form').addEventListener('submit',async ev=>{
 ev.preventDefault();feedback.textContent='Validating…';
 const v={enabled:el('power-enabled').checked,shutdown_battery_percent:Number(el('shutdown-percent').value),shutdown_runtime_minutes:Number(el('shutdown-runtime').value),on_battery_delay_seconds:Number(el('outage-delay').value),recovery_battery_percent:Number(el('recovery-percent').value),recovery_stable_seconds:Number(el('recovery-delay').value),devices:collectDevices()};
 if(v.enabled&&!window.confirm('Enable automated SSH shutdown for approved devices when the UPS crosses the configured threshold?')){feedback.textContent='Not saved.';return;}
 try{const res=await fetch('/api/power',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},credentials:'same-origin',body:JSON.stringify(v)});
 if(!res.ok)throw new Error((await res.text()).trim());feedback.textContent='Power configuration saved.';refreshPowerState();
 }catch(e){feedback.textContent='Could not save: '+e.message;}
 });
 refreshPowerState();setInterval(refreshPowerState,10000);
}
if(page==='/'){refreshDashboard();refreshFirmware();refreshRecent();refreshPowerState();setInterval(refreshDashboard,10000);setInterval(refreshFirmware,60000);setInterval(refreshRecent,60000);setInterval(refreshPowerState,10000);}
if(page==='/events'){refreshEvents();el('range').addEventListener('change',refreshEvents);el('refresh-events').addEventListener('click',refreshEvents);}
if(page==='/settings') startSettings();

if(page==='/power')startPower();
