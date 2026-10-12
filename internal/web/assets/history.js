'use strict';
// Simple dependency-free SVG charts. X positions are based on real UTC time,
// and null samples create visible gaps, never interpolated measurements.
(() => {
const ns='http://www.w3.org/2000/svg';
const $=id=>document.getElementById(id);
const svg=(name,attributes={})=>{const v=document.createElementNS(ns,name);for(const [k,val] of Object.entries(attributes))v.setAttribute(k,String(val));return v;};
function draw(root,report,series,percent){
 root.replaceChildren();
 const W=960,H=266,L=56,R=18,T=23,B=42,w=W-L-R,h=H-T-B;
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
   if(!p.observed||typeof v!=='number'||!Number.isFinite(v)){open=false;continue;}
   const xp=x(t).toFixed(2),yp=y(v).toFixed(2);
   path+=(open?'L':'M')+xp+' '+yp+' ';open=true;
  }
  if(path)s.append(svg('path',{d:path,fill:'none',stroke:entry.color,'stroke-width':2,'stroke-linecap':'round','stroke-linejoin':'round'}));
 }
 root.append(s);
 if(!values.length){const p=document.createElement('p');p.className='history-empty';p.textContent='No stored measurements for this period. Recording begins after this version is installed.';root.append(p);}
}
// Third chart: battery charge, runtime (right axis), and observed mains status.
// Never connect observations across unsampled buckets. Event-sourced battery
// values are shown as dots because transitions cannot reconstruct a trend.
function drawBattery(report){
 const root=$('battery-chart');
 draw(root,report,[{key:'battery_percent',color:'#24986b',allowEvent:true}],true);
 const chart=root.querySelector('svg');if(!chart)return;
 chart.setAttribute('aria-label','UPS battery percentage, runtime minutes and observed mains status');
 const W=960,H=266,L=56,R=18,T=23,B=42,w=W-L-R,h=H-T-B;
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
 const runtimeValues=report.points.map(p=>p.runtime_minutes).filter(v=>typeof v==='number'&&Number.isFinite(v));
 if(runtimeValues.length){
  const max=Math.max(10,Math.ceil(Math.max(...runtimeValues)/10)*10);
  let d='',continuous=false;
  for(const p of report.points){
   const v=p.runtime_minutes;
   if(typeof v!=='number'||!Number.isFinite(v)||(!p.observed&&!p.runtime_from_event)){continuous=false;continue;}
   const xp=x(p.time).toFixed(2),yp=(T+h-v/max*h).toFixed(2);
   if(p.runtime_from_event){chart.append(svg('circle',{cx:xp,cy:yp,r:3,fill:'#aa6bba'}));continuous=false;continue;}
   d+=(continuous?'L':'M')+xp+' '+yp+' ';continuous=true;
  }
  if(d)chart.append(svg('path',{d,fill:'none',stroke:'#aa6bba','stroke-width':2}));
  for(let i=0;i<=4;i++){
   const label=svg('text',{x:W-1,y:y(i*25)+4,'text-anchor':'end',fill:'#8c5b96','font-size':11});
   label.textContent=Math.round(max*i/4)+' min';chart.append(label);
  }
 }
 for(const p of report.points){
  if(!p.battery_from_event||typeof p.battery_percent!=='number')continue;
  if(!p.observed)chart.append(svg('circle',{cx:x(p.time),cy:y(p.battery_percent),r:3.5,fill:'#24986b',stroke:'#fff','stroke-width':1}));
 }
}
function summary(report){
 const points=report.points||[],measured=points.filter(p=>p.observed).length;
 const interruptions=(report.events||[]).filter(e=>e.type==='power_lost'||e.type==='on_battery_detected').length;
 $('history-summary').textContent=`${measured} of ${points.length} fifteen-minute time slots measured · ${interruptions} observed power-loss events. Missing slots remain blank; recorded event measurements appear as individual points.`;
}
async function load(){
 const days=$('history-days').value;
 $('history-message').textContent='Loading retained readings…';
 try{
  const response=await fetch(`/api/history?days=${encodeURIComponent(days)}`,{credentials:'same-origin',cache:'no-store'});
  if(response.status===401){location.assign('/login');return;}
  if(!response.ok)throw new Error(`History request failed (${response.status})`);
  const report=await response.json();
  draw($('voltage-chart'),report,[{key:'input_volts',color:'#1978ad'},{key:'output_volts',color:'#20916a'}],false);
  draw($('load-chart'),report,[{key:'load_percent',color:'#b57922'}],true);
  drawBattery(report);
  summary(report);
  $('history-message').textContent='Data is spaced by actual time. Yellow regions mark observed battery operation from event logs; outages without UPS measurements remain gaps.';
 }catch(err){$('history-message').textContent=`Could not read history: ${err.message}`;}
}
$('history-days').addEventListener('change',load);
$('history-reload').addEventListener('click',load);
load();
})();
