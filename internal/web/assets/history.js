'use strict';
// Simple dependency-free SVG charts. X positions are based on real UTC time,
// and null samples create visible gaps, never interpolated measurements.
(() => {
const ns='http://www.w3.org/2000/svg';
const $=id=>document.getElementById(id);
const svg=(name,attributes={})=>{const v=document.createElementNS(ns,name);for(const [k,val] of Object.entries(attributes))v.setAttribute(k,String(val));return v;};
// Chart geometry shared by draw and drawBattery.
const W=960,H=266,L=56,R=18,T=23,B=42,w=W-L-R,h=H-T-B;
function draw(root,report,series,percent){
 root.replaceChildren();
 const s=svg('svg',{viewBox:`0 0 ${W} ${H}`,role:'img','aria-label':percent?'UPS load time series':'UPS voltage time series'});
 const from=new Date(report.from).getTime(),to=new Date(report.to).getTime();
 const x=time=>L+(Math.max(from,Math.min(to,time))-from)/(to-from)*w;
 let values=[];for(const p of report.points)for(const entry of series)if(typeof p[entry.key]==='number')values.push(p[entry.key]);
 const min=percent?0:(values.length?Math.max(0,Math.floor((Math.min(...values)-5)/10)*10):0);
 const max=percent?100:(values.length?Math.max(min+10,Math.ceil((Math.max(...values)+5)/10)*10):250);
 const y=v=>T+h-(v-min)/(max-min)*h;
 // Grid and axis labels.
 for(let i=0;i<=4;i++){
  const v=min+(max-min)*i/4,yp=y(v);
  s.append(svg('line',{x1:L,y1:yp,x2:W-R,y2:yp,stroke:'#e0e7ed','stroke-width':1}));
  const label=svg('text',{x:L-10,y:yp+4,'text-anchor':'end',fill:'#607787','font-size':12});label.textContent=percent?Math.round(v)+'%':v.toFixed(0)+' V';s.append(label);
 }
 for(let i=0;i<=6;i++){
  const time=from+(to-from)*i/6,xp=x(time),date=new Date(time);
  s.append(svg('line',{x1:xp,y1:T,x2:xp,y2:T+h,stroke:'#eef2f4'}));
  const label=svg('text',{x:xp,y:H-14,'text-anchor':i===0?'start':i===6?'end':'middle',fill:'#607787','font-size':12});
  label.textContent=date.toLocaleDateString(undefined,{month:'short',day:'numeric'});s.append(label);
 }
 // Highlight spans whose observed event history says the UPS is on battery.
 let started=null;
 const firstPower=(report.events||[]).find(e=>e.type==='power_lost'||e.type==='on_battery_detected'||e.type==='power_restored');
 if(firstPower&&firstPower.type==='power_restored')started=from;// power was already lost before the window
 for(const event of report.events||[]){
  const t=new Date(event.time).getTime();
  if(event.type==='power_lost'||event.type==='on_battery_detected'){
   if(started===null)started=Math.max(from,t);
  }else if(event.type==='power_restored'&&started!==null){
   let left=x(started),right=x(t);
   if(right>left)s.append(svg('rect',{x:left,y:T,width:right-left,height:h,fill:'#e9a543','fill-opacity':.11}));
   started=null;
  }
 }
 if(started!==null){let left=x(started);s.append(svg('rect',{x:left,y:T,width:Math.max(0,x(to)-left),height:h,fill:'#e9a543','fill-opacity':.11}));}
 // Break lines explicitly on a missing sample or unsupported metric.
 for(const entry of series){
  let path='',open=false;
  for(const p of report.points){
   const v=p[entry.key],t=new Date(p.time).getTime();
   if(!p.observed||typeof v!=='number'||!Number.isFinite(v)||(entry.key==='battery_percent'&&p.battery_from_event)){open=false;continue;}
   const xp=x(t).toFixed(2),yp=y(v).toFixed(2);
   path+=(open?'L':'M')+xp+' '+yp+' ';open=true;
  }
  if(path)s.append(svg('path',{d:path,fill:'none',stroke:entry.color,'stroke-width':2,'stroke-linecap':'round','stroke-linejoin':'round'}));
 }
 root.append(s);
 if(!values.length){const p=document.createElement('p');p.className='history-empty';p.textContent='No stored measurements for this period. Measurements start when the bridge first runs.';root.append(p);}
}
// Third chart: battery charge, runtime (right axis), and observed mains status.
// Never connect observations across unsampled buckets. Event-sourced battery
// values are shown as dots because transitions cannot reconstruct a trend.
function drawBattery(report){
 const root=$('battery-chart');
 draw(root,report,[{key:'battery_percent',color:'#24986b'}],true);
 const chart=root.querySelector('svg');if(!chart)return;
 chart.setAttribute('aria-label','UPS battery percentage, runtime minutes and observed mains status');
 const from=new Date(report.from).getTime(),to=new Date(report.to).getTime();
 const x=t=>L+(new Date(t).getTime()-from)/(to-from)*w;
 const y=v=>T+h-(v/100)*h;
 // Observed POWER=ONLINE means mains is present, not proof that the server
 // or any remote host had continuous uptime.
 let path='',open=false;
 for(const p of report.points){
  const v=p.observed?(p.power==='ONLINE'?100:p.power==='ON_BATTERY'?0:null):null;
  if(v===null){open=false;continue;}
  path+=(open?'L':'M')+x(p.time).toFixed(2)+' '+y(v).toFixed(2)+' ';open=true;
 }
 if(path)chart.append(svg('path',{d:path,fill:'none',stroke:'#4775bb','stroke-width':2,'stroke-dasharray':'5 4'}));
 const validRuntime=v=>typeof v==='number'&&Number.isFinite(v)&&v>=0&&v<=100000;
 let runtimePeak=-1;
 for(const p of report.points)if(validRuntime(p.runtime_minutes)&&p.runtime_minutes>runtimePeak)runtimePeak=p.runtime_minutes;
 for(const e of report.events||[]){const v=e.details&&e.details.runtime_minutes;if(validRuntime(v)&&v>runtimePeak)runtimePeak=v;}
 const hasRuntime=runtimePeak>=0,runtimeMax=hasRuntime?Math.max(10,Math.ceil(runtimePeak/10)*10):0;
 if(hasRuntime){
  let d='',continuous=false;
  for(const p of report.points){
   const v=p.runtime_minutes;
   if(typeof v!=='number'||!Number.isFinite(v)||!p.observed||p.runtime_from_event){continuous=false;continue;}
   const xp=x(p.time).toFixed(2),yp=(T+h-v/runtimeMax*h).toFixed(2);
   d+=(continuous?'L':'M')+xp+' '+yp+' ';continuous=true;
  }
  if(d)chart.append(svg('path',{d,fill:'none',stroke:'#aa6bba','stroke-width':2}));
  for(let i=0;i<=4;i++){
   const label=svg('text',{x:W-1,y:y(i*25)+4,'text-anchor':'end',fill:'#8c5b96','font-size':11});
   label.textContent=Math.round(runtimeMax*i/4)+' min';chart.append(label);
  }
 }
 // The actual time of an event is more precise than its fifteen-minute
 // sample bucket. Draw an isolated dot at that recorded timestamp even when
 // another sample occupies the same bucket.
 let eventDots=0;
 for(const event of report.events||[]){
  const t=new Date(event.time).getTime();
  if(!Number.isFinite(t)||t<from||t>to)continue;
  const details=event.details||{};
  const b=details.battery_percent;
  if(typeof b==='number'&&Number.isFinite(b)&&b>=0&&b<=100){
   const dot=svg('circle',{cx:x(event.time),cy:y(b),r:event.type==='battery_fully_charged'?5:3.5,fill:'#24986b',stroke:'#fff','stroke-width':1});
   const tip=svg('title');tip.textContent=event.type+' · Battery '+b+'%';dot.append(tip);chart.append(dot);eventDots++;
  }
  const runtime=details.runtime_minutes;
  if(validRuntime(runtime)&&hasRuntime){
   const dot=svg('circle',{cx:x(event.time),cy:T+h-runtime/runtimeMax*h,r:3,fill:'#aa6bba'});
   const tip=svg('title');tip.textContent=event.type+' · Runtime '+runtime+' minutes';dot.append(tip);chart.append(dot);eventDots++;
  }
 }
 // Recorded event points are real measurements, so the graph is not empty.
 if(eventDots){const empty=root.querySelector('.history-empty');if(empty)empty.remove();}
}
function summary(report){
 const points=report.points||[],measured=points.filter(p=>p.observed).length;
 const interruptions=(report.events||[]).filter(e=>e.type==='power_lost'||e.type==='on_battery_detected').length;
 $('history-summary').textContent=`${measured} of ${points.length} fifteen-minute time slots measured · ${interruptions} observed power-loss events. Missing slots remain blank; recorded event measurements appear as individual points.`;
}
// Fetches a report; an expired session is sent to the login page by getJSON.
const fetchReport=(url,label)=>getJSON(url,status=>`${label} request failed (${status})`);
async function load(){
 const days=$('history-days').value;
 $('history-message').textContent='Loading retained readings…';
 try{
  const report=await fetchReport(`/api/history?days=${encodeURIComponent(days)}`,'History');
  draw($('voltage-chart'),report,[{key:'input_volts',color:'#1978ad'},{key:'output_volts',color:'#20916a'}],false);
  draw($('load-chart'),report,[{key:'load_percent',color:'#b57922'}],true);
  summary(report);
  $('history-message').textContent='Data is spaced by actual time. Yellow regions mark observed battery operation from event logs; outages without UPS measurements remain gaps.';
 }catch(err){$('history-message').textContent=`Could not read history: ${err.message}`;}
}
async function loadBattery() {
 const days=$('battery-history-days').value;
 const message=$('battery-history-message');
 message.textContent='Loading battery and event history…';
 try {
  const report=await fetchReport(`/api/history/battery?days=${encodeURIComponent(days)}`,'Battery history');
  drawBattery(report);
  message.textContent='30-day sampled readings plus recorded power events across the selected period. Older unmeasured periods remain blank.';
 }catch(err){message.textContent='Could not load battery history: '+err.message;}
}
$('battery-history-days').addEventListener('change',loadBattery);
$('battery-history-reload').addEventListener('click',loadBattery);
loadBattery();
$('history-days').addEventListener('change',load);
$('history-reload').addEventListener('click',load);
load();
})();
