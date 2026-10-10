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
  const dot=document.createElement('span');dot.className='dot' + (e.level==='warning'?' warning':'');
  const info=document.createElement('div');const title=document.createElement('div');title.className='event-name';title.textContent=e.type.replaceAll('_',' ');
  const desc=document.createElement('div');desc.className='event-detail';
  const details=e.details||{};desc.textContent=[details.status,typeof details.battery_percent==='number'?details.battery_percent+'% battery':'',details.reason||''].filter(Boolean).join(' · ');
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
if(page==='/'){refreshDashboard();refreshRecent();setInterval(refreshDashboard,10000);setInterval(refreshRecent,60000);}
if(page==='/events'){refreshEvents();el('range').addEventListener('change',refreshEvents);el('refresh-events').addEventListener('click',refreshEvents);}
if(page==='/settings') startSettings();
