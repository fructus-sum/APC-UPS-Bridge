'use strict';
// Helpers shared by every page script.
const csrf = document.querySelector('meta[name="csrf-token"]')?.content;
// Loads JSON; an expired session sends the browser to the login page.
async function getJSON(path, failure = status => 'Request failed (' + status + ')') {
 const res = await fetch(path, {cache:'no-store',credentials:'same-origin'});
 if (res.status === 401) { location.assign('/login'); throw new Error('Session expired'); }
 if (!res.ok) throw new Error(failure(res.status));
 return res.json();
}
// Sends a POST (JSON body optional) with the CSRF token; returns the response.
async function postJSON(path, body) {
 const headers = {'X-CSRF-Token':csrf};
 if (body !== undefined) headers['Content-Type'] = 'application/json';
 const res = await fetch(path, {method:'POST',credentials:'same-origin',headers,body:body === undefined ? undefined : JSON.stringify(body)});
 if (!res.ok) throw new Error((await res.text()).trim());
 return res;
}
// Runs fn every ms while the tab is visible; refreshes at once when it returns.
function every(ms, fn) {
 let timer = null;
 const start = () => { timer = setInterval(fn, ms); };
 if (!document.hidden) start();
 document.addEventListener('visibilitychange', () => {
  if (document.hidden) { clearInterval(timer); timer = null; }
  else if (timer === null) { fn(); start(); }
 });
}
