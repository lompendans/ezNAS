const $ = (id) => document.getElementById(id);
let currentPath = "";
let passwordRequired = false;
let pendingSharePath = "";
const fmtSize = (n) => { if (n == null || n < 0) return "—"; if (n < 1024) return n + " B"; const units=["KB","MB","GB","TB"]; let v=n; let i=-1; do {v/=1024;i++;} while(v>=1024&&i<units.length-1); return v.toFixed(v>=100?0:v>=10?1:2)+" "+units[i]; };
const fmtDate = (s) => { const d=new Date(s); return isNaN(d)?"—":d.toLocaleString(); };
async function api(url, opts={}) {
  const res=await fetch(url,{credentials:"same-origin",...opts,headers:opts.body instanceof FormData?opts.headers:{"Content-Type":"application/json",...(opts.headers||{})}});
  const type=res.headers.get("content-type")||"";
  const data=type.includes("json")?await res.json():null;
  if(res.status===401){showLogin();throw new Error(data?.error||"Authentication required");}
  if(!res.ok) throw new Error(data?.error||"Request failed ("+res.status+")");
  return data;
}
function notice(msg,bad=false){$("notice").textContent=msg;$("notice").classList.toggle("hidden",!msg);$("notice").classList.toggle("bad",bad);}
function showLogin(){if(!passwordRequired)return;$("login").classList.remove("hidden");$("app").classList.add("hidden");$("logout").classList.remove("hidden");}
function hideLogin(){$("login").classList.add("hidden");$("app").classList.remove("hidden");}
let installedVersion = "dev";
function versionParts(v){return String(v||"").replace(/^v/i,"").split(/[+-]/,1)[0].split(".").map(x=>{const n=parseInt(x,10);return Number.isFinite(n)?n:0;});}
function isNewerVersion(latest,current){if(!latest||!current||current==="dev"||current==="unknown")return false;const a=versionParts(latest),b=versionParts(current);for(let i=0;i<Math.max(a.length,b.length);i++){const x=a[i]||0,y=b[i]||0;if(x!==y)return x>y;}return false;}
async function checkForUpdates(manual=false){try{const res=await fetch("https://api.github.com/repos/lompendans/ezNAS/releases/latest",{headers:{"Accept":"application/vnd.github+json"},cache:"no-store"});if(!res.ok)throw new Error("GitHub returned HTTP "+res.status);const release=await res.json();if(!release.tag_name||release.draft||release.prerelease)return;const latest=release.tag_name;if(isNewerVersion(latest,installedVersion)){if(localStorage.getItem("eznas-update-snooze")===latest&&!manual)return;$("update-title").textContent="ezNAS "+latest+" is available";$("update-details").textContent="You're running "+installedVersion+". Install the verified update or review the release notes.";$("update-link").href=release.html_url||"https://github.com/lompendans/ezNAS/releases/latest";$("update-banner").classList.remove("hidden");$("auto-update").checked=localStorage.getItem("eznas-auto-update")==="true";if($("auto-update").checked&&localStorage.getItem("eznas-auto-attempt")!==latest){localStorage.setItem("eznas-auto-attempt",latest);applyUpdate(true);}}else if(manual){notice("You're running the latest stable ezNAS release ("+installedVersion+").");}}catch(e){if(manual)notice("Couldn't check for updates right now. Check your internet connection and try again.",true);}}
async function applyUpdate(automatic=false){const button=$("update-now");button.disabled=true;button.textContent="Preparing…";try{const result=await api("/api/update",{method:"POST",body:"{}"});notice(result.message||"Update check complete.");if(result.updated){button.textContent="Restarting…";$("update-details").textContent="The verified update is being installed. ezNAS will briefly disconnect while it restarts.";}else{button.disabled=false;button.textContent="Install update";if(automatic)notice(result.message||"No update needed.");}}catch(e){button.disabled=false;button.textContent="Retry update";notice(e.message,true);if(automatic)localStorage.removeItem("eznas-auto-attempt");}}
$("update-now").addEventListener("click",()=>applyUpdate(false));
$("auto-update").addEventListener("change",e=>{localStorage.setItem("eznas-auto-update",String(e.target.checked));if(!e.target.checked)localStorage.removeItem("eznas-auto-attempt");});
$("update-later").addEventListener("click",()=>{const m=$("update-title").textContent.match(/ezNAS (v?[^ ]+) is available/);if(m)localStorage.setItem("eznas-update-snooze",m[1]);$("update-banner").classList.add("hidden");});
async function boot(){try{const s=await api("/api/status");installedVersion=s.version||"unknown";passwordRequired=s.passwordRequired;checkForUpdates();setInterval(()=>checkForUpdates(),6*60*60*1000);$("connection").textContent="Server online";$("server-host").textContent=s.hostname||"Unknown device";$("server-meta").textContent=(s.os||"server")+" · "+(s.address||((s.hostname||"localhost")+":"+s.port));$("tls-status").textContent=s.https?"HTTPS is enabled for this server.":"HTTPS is not configured; connections use HTTP unless protected by a reverse proxy.";$("backup-path").textContent="Backups are stored on this NAS device. Keep an additional copy on another drive or device.";if(passwordRequired){const auth=await api("/api/login");if(auth.passwordRequired){try{await api("/api/files");hideLogin();}catch(e){showLogin();return;}}}await loadFiles();}catch(e){if(passwordRequired)showLogin();else{notice(e.message,true);$("connection").textContent="Connection error";}}}
$("login-form").addEventListener("submit",async e=>{e.preventDefault();$("login-error").textContent="";try{await api("/api/login",{method:"POST",body:JSON.stringify({password:$("password").value})});$("password").value="";hideLogin();await loadFiles();}catch(err){$("login-error").textContent=err.message;}});
$("logout").addEventListener("click",async()=>{try{await api("/api/logout",{method:"POST",body:"{}"});}catch{}showLogin();});
function setView(view){document.querySelectorAll(".nav").forEach(b=>b.classList.toggle("active",b.dataset.view===view));["files","search","storage","trash","backups","shares"].forEach(v=>$(v+"-view").classList.toggle("hidden",v!==view));const titles={files:["Your files","Simple storage, right on your own device."],search:["Search files","Find items by name across your storage."],storage:["Storage","See the storage location configured for ezNAS."],trash:["Recycle bin","Restore deleted files or permanently remove them."],backups:["Backups","Create and download ZIP snapshots of your storage."],shares:["Shared links","Manage temporary, read-only links for family and friends."]};$("heading").textContent=titles[view][0];$("subheading").textContent=titles[view][1];if(view==="storage")loadStorage();if(view==="files")loadFiles();if(view==="trash")loadTrash();if(view==="backups")loadBackups();if(view==="shares")loadShares();}
document.querySelectorAll(".nav").forEach(b=>b.addEventListener("click",()=>setView(b.dataset.view)));
function renderCrumbs(){const box=$("breadcrumbs");box.replaceChildren();const root=document.createElement("button");root.className="crumb"+(!currentPath?" current":"");root.textContent="All files";root.onclick=()=>{currentPath="";loadFiles();};box.append(root);let built="";for(const part of currentPath.split("/").filter(Boolean)){const sep=document.createElement("span");sep.className="crumb-sep";sep.textContent="/";box.append(sep);built+=(built?"/":"")+part;const btn=document.createElement("button");btn.className="crumb"+(built===currentPath?" current":"");btn.textContent=part;const dest=built;btn.onclick=()=>{currentPath=dest;loadFiles();};box.append(btn);}}
function makeFileRow(item, target, searchMode=false){const tr=document.createElement("tr");const nameTd=document.createElement("td");const line=document.createElement("div");line.className="file-name";const icon=document.createElement("span");icon.className="file-icon"+(item.directory?" folder":"");icon.textContent=item.directory?"▰":iconFor(item.name);const name=document.createElement("button");name.className="name-button";name.textContent=item.name;name.title=item.name;name.onclick=()=>{if(item.directory){currentPath=item.path;setView("files");}else download(item.path);};line.append(icon,name);nameTd.append(line);tr.append(nameTd);const size=document.createElement("td");size.textContent=item.directory?"Folder":fmtSize(item.size);tr.append(size);const date=document.createElement("td");date.textContent=fmtDate(item.modified);tr.append(date);const actions=document.createElement("td");const row=document.createElement("div");row.className="row-actions";if(!item.directory){
const share=document.createElement("button");share.textContent="Share";share.onclick=()=>openShareDialog(item);row.append(share);
const dl=document.createElement("button");dl.textContent="Download";dl.onclick=()=>download(item.path);row.append(dl);
}const ren=document.createElement("button");ren.textContent="Rename";ren.onclick=()=>renameItem(item);row.append(ren);const del=document.createElement("button");del.textContent="Move to recycle bin";del.onclick=()=>deleteItem(item);row.append(del);actions.append(row);tr.append(actions);return tr;}
function iconFor(name){const ext=name.split(".").pop().toLowerCase();if(["png","jpg","jpeg","gif","webp","svg"].includes(ext))return "▧";if(["mp3","wav","ogg","flac"].includes(ext))return "♫";if(["mp4","mov","mkv","webm"].includes(ext))return "▷";if(["txt","md","log","json","yaml","yml","csv"].includes(ext))return "≡";if(["zip","7z","rar","gz","tar"].includes(ext))return "▤";return "▱";}
async function loadFiles(){renderCrumbs();$("file-list").replaceChildren();try{const data=await api("/api/files?path="+encodeURIComponent(currentPath));for(const item of data.items)$("file-list").append(makeFileRow(item,$("file-list")));$("empty").classList.toggle("hidden",data.items.length>0);notice("");}catch(e){notice(e.message,true);}}
function download(path){const a=document.createElement("a");a.href="/api/download?path="+encodeURIComponent(path);a.download="";document.body.append(a);a.click();a.remove();}
function openModal(title,label,initial,submitText,callback){$("modal-title").textContent=title;$("modal-label").textContent=label;$("modal-input").value=initial||"";$("modal-form").dataset.submit=submitText;$("modal").classList.remove("hidden");$("modal-input").focus();$("modal-form").onsubmit=async e=>{e.preventDefault();try{await callback($("modal-input").value.trim());$("modal").classList.add("hidden");await loadFiles();notice("");}catch(err){notice(err.message,true);}};}
$("modal-close").onclick=$("modal-cancel").onclick=()=>$("modal").classList.add("hidden");
$("new-folder").onclick=()=>openModal("Create folder","Folder name","","Create",async name=>{if(!name)throw new Error("Enter a folder name");await api("/api/mkdir",{method:"POST",body:JSON.stringify({path:currentPath,name})});});
async function renameItem(item){openModal("Rename item","New name",item.name,"Rename",async name=>{if(!name)throw new Error("Enter a name");await api("/api/rename",{method:"POST",body:JSON.stringify({path:item.path,name})});});}
async function deleteItem(item){if(!confirm("Move “"+item.name+"” to the recycle bin?"))return;try{await api("/api/delete",{method:"POST",body:JSON.stringify({path:item.path})});await loadFiles();notice("Moved "+item.name+" to the recycle bin.");}catch(e){notice(e.message,true);}}
async function uploadFiles(files){if(!files.length)return;const total=files.length;$("progress").classList.remove("hidden");let done=0;try{for(const file of files){$("progress-text").textContent="Uploading "+file.name+" ("+(done+1)+" of "+total+")";const form=new FormData();form.append("path",currentPath);form.append("file",file,file.name);await new Promise((resolve,reject)=>{const xhr=new XMLHttpRequest();xhr.open("POST","/api/upload");xhr.withCredentials=true;xhr.upload.onprogress=e=>{if(e.lengthComputable)$("progress-bar").style.width=Math.round(e.loaded/e.total*100)+"%";};xhr.onload=()=>{let d={};try{d=JSON.parse(xhr.responseText);}catch{}if(xhr.status>=200&&xhr.status<300)resolve();else reject(new Error(d.error||"Upload failed"));};xhr.onerror=()=>reject(new Error("Network error during upload"));xhr.send(form);});done++;$("progress-bar").style.width=Math.round(done/total*100)+"%";}notice("Uploaded "+done+" file"+(done===1?"":"s")+".");await loadFiles();}catch(e){notice(e.message,true);}finally{setTimeout(()=>$("progress").classList.add("hidden"),900);}}
$("upload-input").addEventListener("change",e=>{uploadFiles([...e.target.files]);e.target.value="";});
$("refresh").onclick=loadFiles;
const dz=$("dropzone");dz.addEventListener("dragover",e=>{e.preventDefault();dz.classList.add("dragover");});dz.addEventListener("dragleave",()=>dz.classList.remove("dragover"));dz.addEventListener("drop",e=>{e.preventDefault();dz.classList.remove("dragover");uploadFiles([...e.dataTransfer.files]);});
async function doSearch(){const q=$("search-input").value.trim();if(!q)return;const box=$("search-results");box.replaceChildren();try{const d=await api("/api/search?q="+encodeURIComponent(q));const table=document.createElement("table");table.innerHTML="<thead><tr><th>Name</th><th>Size</th><th>Modified</th><th></th></tr></thead>";const body=document.createElement("tbody");d.items.forEach(i=>body.append(makeFileRow(i,body,true)));table.append(body);box.append(table);if(!d.items.length){const p=document.createElement("p");p.className="muted";p.style.padding="20px";p.textContent="No matching files found.";box.append(p);}if(d.limited)notice("Showing the first 500 matches.");else notice("");}catch(e){notice(e.message,true);}}
$("search-button").onclick=doSearch;$("search-input").addEventListener("keydown",e=>{if(e.key==="Enter")doSearch();});
async function loadStorage(){try{const d=await api("/api/storage");$("storage-path").textContent=d.path;$("storage-note").textContent=d.note||"";if(d.totalBytes){const used=d.usedBytes||0,free=d.freeBytes||0;$("storage-used").textContent=fmtSize(used);$("storage-free").textContent=fmtSize(free);$("storage-total").textContent=fmtSize(d.totalBytes);$("storage-meter-fill").style.width=Math.min(100,used/d.totalBytes*100)+"%";}else{$("storage-used").textContent="—";$("storage-free").textContent="—";$("storage-total").textContent="—";$("storage-meter-fill").style.width="0";}}catch(e){notice(e.message,true);}}
boot();
async function loadTrash(){const box=$("trash-list");box.replaceChildren();try{const d=await api("/api/trash");if(!d.items.length){box.textContent="Recycle bin is empty.";return;}const table=document.createElement("table");table.innerHTML="<thead><tr><th>Name</th><th>Original location</th><th>Deleted</th><th></th></tr></thead>";const body=document.createElement("tbody");for(const item of d.items){const tr=document.createElement("tr");for(const val of [item.name+(item.directory?"/":""),item.path,fmtDate(item.deleted)]){const td=document.createElement("td");td.textContent=val;tr.append(td);}const td=document.createElement("td"),row=document.createElement("div");row.className="row-actions";const restore=document.createElement("button");restore.textContent="Restore";restore.onclick=async()=>{try{await api("/api/trash/restore",{method:"POST",body:JSON.stringify({id:item.id})});notice("Restored "+item.name);loadTrash();}catch(e){notice(e.message,true);}};row.append(restore);td.append(row);tr.append(td);body.append(tr);}table.append(body);box.append(table);}catch(e){notice(e.message,true);}}
$("empty-trash").addEventListener("click",async()=>{if(!confirm("Permanently delete every item in the recycle bin? This cannot be undone."))return;try{await api("/api/trash/purge",{method:"POST",body:"{}"});await loadTrash();notice("Recycle bin emptied.");}catch(e){notice(e.message,true);}});
async function loadBackups(){const box=$("backup-list");box.replaceChildren();try{const d=await api("/api/backups");if(!d.items.length){box.textContent="No backups created yet.";return;}const table=document.createElement("table");table.innerHTML="<thead><tr><th>Backup</th><th>Size</th><th>Created</th><th></th></tr></thead>";const body=document.createElement("tbody");for(const item of d.items){const tr=document.createElement("tr");for(const val of [item.name,fmtSize(item.size),fmtDate(item.created)]){const td=document.createElement("td");td.textContent=val;tr.append(td);}const td=document.createElement("td"),a=document.createElement("a");a.className="button secondary";a.textContent="Download";a.href="/api/backups/download?name="+encodeURIComponent(item.name);td.append(a);tr.append(td);body.append(tr);}table.append(body);box.append(table);}catch(e){notice(e.message,true);}}
$("create-backup").addEventListener("click",async()=>{const button=$("create-backup");button.disabled=true;button.textContent="Creating backup…";try{const d=await api("/api/backups",{method:"POST",body:"{}"});await loadBackups();notice("Backup created: "+d.backup.name+" ("+fmtSize(d.backup.size)+")");}catch(e){notice(e.message,true);}finally{button.disabled=false;button.textContent="＋ Create backup";}});

async function openShareDialog(item){
  pendingSharePath=item.path;
  const localHosts=["localhost","127.0.0.1","::1"];
  $("share-local-warning").classList.toggle("hidden",localHosts.includes(location.hostname.toLowerCase()));
  $("share-file-name").textContent=item.name;
  $("share-link-result").classList.add("hidden");
  $("share-url").value="";
  $("share-create").disabled=false;
  $("share-create").textContent="Create share link";
  $("share-modal").classList.remove("hidden");
}
$("share-close").addEventListener("click",()=>$("share-modal").classList.add("hidden"));
$("share-create").addEventListener("click",async()=>{
  const button=$("share-create");button.disabled=true;button.textContent="Creating…";
  try{
    const d=await api("/api/shares",{method:"POST",body:JSON.stringify({path:pendingSharePath,expiresInHours:Number($("share-expiry").value)})});
    const link=location.origin+d.url;
    $("share-url").value=link;
    $("share-link-result").classList.remove("hidden");
    button.textContent="Create another link";
    button.disabled=false;
    try{await navigator.clipboard.writeText(link);notice("Share link created and copied to clipboard. It expires "+fmtDate(d.share.expires_at)+".");}
    catch{notice("Share link created. Copy it from the box before closing this window.");}
    await loadShares();
  }catch(e){notice(e.message,true);button.disabled=false;button.textContent="Retry";}
});
$("share-copy").addEventListener("click",async()=>{
  const value=$("share-url").value;if(!value)return;
  try{await navigator.clipboard.writeText(value);notice("Share link copied.");}
  catch{$("share-url").focus();$("share-url").select();notice("Select the link and copy it manually.");}
});
async function loadShares(){
  const box=$("shares-list");box.replaceChildren();
  try{
    const d=await api("/api/shares");
    if(!d.items.length){box.textContent="No active share links. Open Files and choose Share on a file to create one.";return;}
    const table=document.createElement("table");
    table.innerHTML="<thead><tr><th>File</th><th>Location</th><th>Created</th><th>Expires</th><th></th></tr></thead>";
    const body=document.createElement("tbody");
    for(const item of d.items){
      const tr=document.createElement("tr");
      for(const value of [item.name,item.path,fmtDate(item.created_at),fmtDate(item.expires_at)]){
        const td=document.createElement("td");td.textContent=value;tr.append(td);
      }
      const actionCell=document.createElement("td"),actions=document.createElement("div");actions.className="row-actions";
      const revoke=document.createElement("button");revoke.textContent="Revoke";
      revoke.onclick=async()=>{
        if(!confirm("Revoke the share link for "+item.name+"? Anyone using it will lose access."))return;
        try{await api("/api/shares/revoke",{method:"POST",body:JSON.stringify({id:item.id})});await loadShares();notice("Share link revoked.");}
        catch(e){notice(e.message,true);}
      };
      actions.append(revoke);actionCell.append(actions);tr.append(actionCell);body.append(tr);
    }
    table.append(body);box.append(table);
  }catch(e){notice(e.message,true);}
}

$("refresh-shares").addEventListener("click",loadShares);
