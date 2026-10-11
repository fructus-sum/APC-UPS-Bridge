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
function summary(report){
 const points=report.points||[],measured=points.filter(p=>p.observed).length;
 const interruptions=(report.events||[]).filter(e=>e.type==='power_lost'||e.type==='on_battery_detected').length;
 $('history-summary').textContent=`${measured} of ${points.length} fifteen-minute time slots measured · ${interruptions} observed power-loss events. Missing slots remain blank.`;
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
  summary(report);
  $('history-message').textContent='Data is spaced by actual time. Yellow regions mark observed battery operation from event logs; outages without UPS measurements remain gaps.';
 }catch(err){$('history-message').textContent=`Could not read history: ${err.message}`;}
}
$('history-days').addEventListener('change',load);
$('history-reload').addEventListener('click',load);
load();
})();
