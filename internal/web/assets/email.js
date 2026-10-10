'use strict';
const $=id=>document.getElementById(id);
const csrf=document.querySelector('meta[name="csrf-token"]').content;
const fields={host:'mail-host',port:'mail-port',security:'mail-security',username:'mail-user',password:'mail-password',from:'mail-from',to:'mail-to'};
const message=text=>{$('mail-feedback').textContent=text};
async function fetchStatus(){
 const res=await fetch('/api/email',{credentials:'same-origin',cache:'no-store'});
 if(!res.ok)throw Error('Unable to load email settings');
 const j=await res.json(),s=j.settings,r=j.status;
 $('mail-enabled').checked=s.enabled;
 for(const [key,id] of Object.entries(fields)){if(key!=='password')$(id).value=s[key]??'';}
 $('mail-wait-devices').checked=s.wait_for_devices;
 $('mail-password-status').textContent=s.has_password?'A password is saved. Leave blank to retain it.':'No SMTP password saved.';
 $('mail-state').textContent=s.enabled?'Enabled':'Disabled';$('mail-state').className='state'+(s.enabled?' ok':'');
 $('mail-report').textContent=r.pending?`Report pending for incident ${r.incident_id}. ${r.outages} power interruption(s), ${r.pi_restarts} service restart(s). ${r.last_error||'Awaiting full battery, devices and SMTP.'}`:r.last_report_at?`Last recovery report sent: ${new Date(r.last_report_at).toLocaleString()}`:'No pending outage report.';
}
$('email-form').addEventListener('submit',async ev=>{
 ev.preventDefault();message('Saving…');
 const obj={enabled:$('mail-enabled').checked,wait_for_devices:$('mail-wait-devices').checked};
 for(const [key,id] of Object.entries(fields))obj[key]=key==='port'?Number($(id).value):$(id).value.trim();
 try{const res=await fetch('/api/email',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify(obj)});if(!res.ok)throw Error((await res.text()).trim());$('mail-password').value='';message('Email settings saved.');await fetchStatus();}catch(e){message('Unable to save: '+e.message)}
});
$('mail-test').addEventListener('click',async()=>{
 message('Sending test email…');try{const res=await fetch('/api/email/test',{method:'POST',headers:{'X-CSRF-Token':csrf},credentials:'same-origin'});if(!res.ok)throw Error((await res.text()).trim());message('Test email submitted to SMTP server.')}catch(e){message('Test failed: '+e.message)}
});
fetchStatus().catch(e=>message(e.message));setInterval(()=>fetchStatus().catch(()=>{}),60000);
