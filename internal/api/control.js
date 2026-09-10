'use strict';
const $ = id => document.getElementById(id);
const S = {token:'', config:null, revision:0, catalog:[], descriptors:[], schema:[], credentials:[], status:{sources:[]}, history:[], restart:[]};
const pages = [
 ['Workspace',['overview','providers','accounts','credentials','sources','models']],
 ['Traffic',['groups','routing','health','metrics']],
 ['Environment',['browsers','devices','sessions']],
 ['System',['config','logs','about']]
];
const labels = {overview:'Overview',providers:'Providers',accounts:'Accounts',credentials:'Credentials',sources:'Sources',models:'Models',groups:'Groups',routing:'Routing',health:'Health',metrics:'Metrics',browsers:'Browsers',devices:'Devices',sessions:'Sessions',config:'Configuration',logs:'Activity',about:'About'};
const names = {id:'ID',provider_id:'Provider',account_id:'Account',credential_ref:'Credential',base_url:'Base URL',key_env:'Legacy credential environment variable',account_id_env:'Legacy account ID environment variable',auto_approved:'Allow Auto routing',allow_paid:'Legacy paid-source policy',allow_unknown_cost:'Allow unknown costs',max_inflight:'Concurrent requests',quota_max_inflight:'Shared quota concurrency',quota_domain:'Quota domain',max_input_bytes:'Maximum input bytes',cdp_url:'Browser connection URL',source_kind:'Source type',tools:'Tool capability',local:'Loopback / local transport',paid:'Legacy paid flag'};
const title = text => names[text] || text.replaceAll('_',' ').replace(/\b\w/g,c=>c.toUpperCase());
const clone = value => JSON.parse(JSON.stringify(value));
function h(tag,attrs,...children) {
 const el=document.createElement(tag);
 for(const [key,value] of Object.entries(attrs||{})){
  if(value==null)continue;
  if(key.startsWith('on'))el.addEventListener(key.slice(2).toLowerCase(),value);
  else if(key==='class')el.className=value;
  else if(['value','checked','disabled','open','draggable'].includes(key))el[key]=value;
  else el.setAttribute(key,value);
 }
 for(const child of children.flat(Infinity)){if(child!=null)el.append(child instanceof Node?child:document.createTextNode(String(child)))}
 return el;
}
function message(text,error=false){
 $('notice').textContent=text;$('notice').className=error?'error':'';
 const detail=$('dialog-error');if(detail)detail.textContent=error?text:'';
}
function button(text,action,kind=''){
 return h('button',{type:'button',class:kind,onclick:async function(){
  this.disabled=true;
  try{await action()}catch(error){message(error.message,true)}finally{this.disabled=false}
 }},text);
}
function badge(text,kind=''){return h('span',{class:'badge '+kind},text)}
let fieldID=0;
function field(label,input){
 const id='field-'+(++fieldID);input.id=id;
 return h('div',{class:'field'},h('label',{for:id},label),input);
}
function select(values,value,empty=false){
 const input=h('select',{});
 if(empty)input.append(h('option',{value:''},'Choose…'));
 for(const option of values){const v=typeof option==='string'?option:option.value;input.append(h('option',{value:v},typeof option==='string'?option:option.label))}
 input.value=value??'';return input;
}
async function api(path,options={}){
 if(!S.token)throw Error('Connect with your admin key first.');
 const response=await fetch(path,{...options,headers:{Authorization:'Bearer '+S.token,'Content-Type':'application/json',...(options.headers||{})},cache:'no-store'});
 const data=await response.json();
 if(!response.ok)throw Error(data.error?.message||'Request failed ('+response.status+').');
 return data;
}
async function refresh(){
 const [current,catalog,descriptors,schema,status,credentials,history,evidence]=await Promise.all([
  api('/admin/config'),api('/admin/catalog'),api('/admin/descriptors'),api('/admin/config/schema'),api('/admin/status'),api('/admin/credentials'),api('/admin/config/history'),api('/admin/evidence')
 ]);
 Object.assign(S,{config:current.config,revision:current.revision,restart:current.restart_required||[],catalog,descriptors,schema,status,credentials,history,evidence});
 $('access').textContent='Connected';render();message('Updated at '+new Date().toLocaleTimeString()+'. Configuration revision '+S.revision+'.');
}
function connectView(){
 const key=h('input',{type:'password',autocomplete:'off','aria-label':'Admin key',placeholder:'Admin key'});
 const connect=async()=>{S.token=key.value;key.value='';try{await refresh()}catch(e){S.token='';throw e}};
 key.addEventListener('keydown',e=>{if(e.key==='Enter')buttonAction(connect)});
 return h('section',{class:'connection-panel'},h('h2',{},'Your sources, one place.'),h('p',{},'Manage accounts, models and routing on this gateway.'),field('Admin key',key),button('Connect',connect,'primary'),h('p',{},'The key stays in this tab’s memory. Disconnect or close the tab to clear it.'));
}
async function buttonAction(fn){try{await fn()}catch(e){message(e.message,true)}}
let dialogCleanup=null;
$('dialog').addEventListener('close',()=>{dialogCleanup?.();dialogCleanup=null});
function dialog(name,body,actions=[]){
 dialogCleanup?.();dialogCleanup=null;
 const close=button('Close',()=> $('dialog').close());
 $('dialog-content').replaceChildren(h('div',{class:'dialog-head'},h('h2',{id:'dialog-title'},name),close),h('div',{id:'dialog-error',class:'warning',role:'alert'}),h('div',{class:'dialog-body'},body),h('div',{class:'dialog-footer'},actions));
 if(!$('dialog').open)$('dialog').showModal();
}
function pageHead(name,description,...actions){return h('div',{class:'page-head'},h('div',{},h('h1',{},name),h('p',{},description)),h('div',{class:'actions'},actions))}
function table(headers,rows,empty='No entries yet.'){
 if(!rows.length)return h('div',{class:'table-wrap empty'},empty);
 const head=h('thead',{},h('tr',{},headers.map(x=>h('th',{},x))));
 const body=h('tbody',{},rows.map(row=>h('tr',{},row.map(x=>h('td',{},x??'—')))));
 return h('div',{class:'table-wrap'},h('table',{},head,body));
}
function state(source){
 const status=S.status.sources.find(x=>x.id===source.id);
 if(!status?.enabled)return badge('Disabled');
 if(status.blocked)return badge('Auth / policy block','bad');
 if(Date.parse(status.cooldown)>Date.now())return badge('Cooldown','warn');
 return badge(status.completed?'Healthy':'Not tested',status.completed?'good':'');
}
function diff(a,b,path=''){
 if(JSON.stringify(a)===JSON.stringify(b))return [];
 if(a&&b&&typeof a==='object'&&typeof b==='object'){
  return [...new Set([...Object.keys(a),...Object.keys(b)])].flatMap(k=>diff(a[k],b[k],path?path+'.'+k:k));
 }
 return [{path,before:a,after:b}];
}
function display(value){if(value===undefined)return 'Not set';if(value===null)return 'Inherit';return typeof value==='object'?JSON.stringify(value):String(value)}
function diffTable(changes){return h('div',{class:'diff'},table(['Setting','Before','After'],changes.map(c=>[c.path,h('span',{class:'diff-before'},display(c.before)),h('span',{class:'diff-after'},display(c.after))])))}
async function preview(next,summary,back,base=clone(S.config),revision=S.revision,applied){
 const changes=diff(base,next);
 if(!changes.length){message('No changes to apply.');return}
 const result=await api('/admin/config/preview',{method:'POST',body:JSON.stringify({revision,config:next})});
 const body=[h('p',{},summary),diffTable(changes)];
 if(result.restart_required.length)body.unshift(h('div',{class:'warning'},'Restart required for: '+result.restart_required.join(', ')+'. Other valid source and routing changes apply immediately.'));
 body.push(h('p',{class:'muted'},'Provider, account and source switches affect new requests. Active requests can finish.'));
 dialog('Review changes',body,[button('Back',back||(()=> $('dialog').close())),button('Apply changes',async()=>{
  await api('/admin/config',{method:'PATCH',body:JSON.stringify({revision,config:next,summary})});
  $('dialog').close();await refresh();message('Changes saved. Revision '+S.revision+'.');
  await applied?.();
 },'primary')]);
}
Object.assign(names,{input_usd_per_million:'Input USD per million tokens',output_usd_per_million:'Output USD per million tokens',max_usd_per_million:'Maximum USD per million tokens',require_vision:'Require vision',preferences:'Preferences in priority order'});
function schemaFor(name){return S.schema.find(x=>x.name===name)}
function blank(schema){
 if(schema.nullable)return null;
 if(schema.type==='object')return Object.fromEntries((schema.fields||[]).map(f=>[f.name,blank(f)]));
 if(schema.type==='array')return [];
 if(schema.type==='boolean')return false;
 if(schema.type==='integer'||schema.type==='number')return 0;
 if(schema.type==='datetime')return new Date().toISOString();
 return '';
}
function members(value){
 let selected=[...(value||[])];const list=h('ul',{class:'member-list'});let drag='';
 function draw(){
  const ids=[...selected,...S.config.sources.map(s=>s.id).filter(id=>!selected.includes(id))];
  list.replaceChildren(...ids.map(id=>{
   const checked=selected.includes(id);
   const input=h('input',{type:'checkbox',checked,onchange:()=>{selected=checked?selected.filter(x=>x!==id):[...selected,id];draw()}});
   const move=offset=>{const i=selected.indexOf(id),j=i+offset;if(i>=0&&j>=0&&j<selected.length){[selected[i],selected[j]]=[selected[j],selected[i]];draw()}};
   const row=h('li',{draggable:checked,ondragstart:()=>{drag=id},ondragover:e=>e.preventDefault(),ondrop:e=>{e.preventDefault();const a=selected.indexOf(drag),b=selected.indexOf(id);if(a>=0&&b>=0){selected.splice(a,1);selected.splice(b,0,drag);draw()}}},
    h('label',{},input,id),checked?[button('Up',()=>move(-1)),button('Down',()=>move(1))]:[]);
   return row;
  }));
 }
 draw();return {element:list,read:()=>selected};
}
function formField(schema,value,label=schema.name){
 if(schema.type==='object'){
  const form=objectForm(schema.fields,value||{});
  return {element:h('details',{open:true},h('summary',{},title(label),' ',schema.restart_required?badge('Restart required','warn'):null),form.element),read:form.read};
 }
 if(schema.type==='array'){
  if(schema.name==='preferences'){
   const root=h('div',{class:'array'}),rows=h('div',{});let selected=[...(value||[])];
   const draw=()=>rows.replaceChildren(...selected.map((entry,i)=>{
    const choice=select(schema.item.enum,entry);choice.setAttribute('aria-label','Preference '+(i+1));choice.onchange=()=>{selected[i]=choice.value};
    const move=offset=>{const j=i+offset;if(j>=0&&j<selected.length){[selected[i],selected[j]]=[selected[j],selected[i]];draw()}};
    return h('div',{class:'array-row'},choice,button('Up',()=>move(-1)),button('Down',()=>move(1)),button('Remove',()=>{selected.splice(i,1);draw()}));
   }));
   root.append(h('h3',{},title(label)),h('p',{class:'muted'},'Auto, latency and load-balance compare these preferences first, in order. Unknown latency or price ranks last. A cost ceiling requires both declared input and output rates; it is not a per-request spending limit.'),rows,button('Add preference',()=>{const next=schema.item.enum.find(x=>!selected.includes(x));if(next){selected.push(next);draw()}}));draw();return {element:root,read:()=>selected};
  }
  if(schema.name==='sources'&&schema.item.type==='string'){const list=members(value);return {element:h('div',{class:'array'},h('h3',{},'Sources and order'),h('p',{class:'muted'},'Drag selected sources, or use Up and Down. Fallback uses this order.'),list.element),read:list.read}}
  const root=h('div',{class:'array'}),rows=h('div',{});let entries=[];
  const add=v=>{
   const entry=formField(schema.item,v,schema.item.type==='object'?((v&&v.id)||'New item'):'');
   const row=h('div',{class:'array-row'},entry.element,button('Remove',()=>{entries=entries.filter(x=>x!==entry);row.remove()},'danger'));
   entries.push(entry);rows.append(row);
  };
  root.append(h('h3',{},title(label)),rows,button('Add item',()=>add(blank(schema.item))));
  for(const item of value||[])add(item);
  return {element:root,read:()=>entries.map(x=>x.read())};
 }
 let input;
 if(schema.type==='boolean'&&!schema.nullable){
  input=h('input',{type:'checkbox',checked:!!value});
  return {element:h('label',{class:'boolean'},input,title(label)),read:()=>input.checked};
 }
 if(schema.type==='boolean'){
  input=select([{value:'',label:'Inherit'},{value:'true',label:'Yes'},{value:'false',label:'No'}],value==null?'':String(value));
  return {element:field(title(label),input),read:()=>input.value===''?null:input.value==='true'};
 }
 let choices=schema.enum;
 if(['provider','provider_id'].includes(schema.name))choices=[...new Set([...S.catalog.map(p=>p.id),...(S.config.providers||[]).map(p=>p.id),...(value?[value]:[])])].sort();
 if(schema.name==='adapter')choices=S.descriptors.map(d=>d.id);
 if(schema.name==='account_id')choices=(S.config.accounts||[]).map(a=>a.id);
 if(schema.name==='credential_ref')choices=[...new Set([...S.credentials.map(c=>c.id),...(value?[value]:[])])];
 if(schema.name==='browser_profile_id')choices=(S.config.browser_profiles||[]).map(p=>p.id);
 if(choices)input=select(choices,value,true);
 else input=h('input',{type:['integer','number'].includes(schema.type)?'number':'text',value:value??'',step:schema.type==='integer'?'1':schema.type==='number'?'any':null});
 return {element:field(title(label)+(schema.restart_required?' (restart required)':''),input),read:()=>{
  if(schema.nullable&&input.value.trim()==='')return null;
  if(schema.type==='number'){const n=Number(input.value);if(!Number.isFinite(n)||n<0)throw Error(title(label)+' must be a nonnegative number.');return n}
  if(schema.type==='integer'){const n=Number(input.value);if(!Number.isSafeInteger(n))throw Error(title(label)+' must be a whole number.');return n}
  return input.value;
 }};
}
function objectForm(fields,value){
 const entries=fields.map(schema=>({schema,field:formField(schema,value[schema.name])}));
 return {element:h('div',{class:'form-grid'},entries.map(x=>x.field.element)),read:()=>Object.fromEntries(entries.map(x=>[x.schema.name,x.field.read()]))};
}
function edit(kind,item,create=false){
 const base=clone(S.config),revision=S.revision,form=objectForm(schemaFor(kind).item.fields,item);
 let membership;
 if(kind==='sources'){
  membership=h('div',{class:'array'},h('h3',{},'Group membership'));
  for(const g of base.groups){
   const input=h('input',{type:'checkbox',checked:g.sources.includes(item.id),'data-group':g.id});
   membership.append(h('label',{class:'boolean'},input,g.id));
  }
 }
 function show(){
  dialog((create?'Add ':'Edit ')+title(kind).replace(/s$/,'')+(create?'':' / '+item.id),[form.element,membership],[
   button('Cancel',()=> $('dialog').close()),
   button('Review changes',async()=>{
    const next=clone(base),value=form.read();
    next[kind]=next[kind]||[];
    if(create)next[kind].push(value);else next[kind][next[kind].findIndex(x=>x.id===item.id)]=value;
    if(kind==='accounts'&&!(next.providers||[]).some(p=>p.id===value.provider_id)){
     next.providers=next.providers||[];next.providers.push({id:value.provider_id,enabled:true,auto_approved:false,pool_strategy:'round-robin'});
    }
    if(membership)for(const input of membership.querySelectorAll('input')){
     const group=next.groups.find(g=>g.id===input.dataset.group);
     if(input.checked){
      if(group.sources.includes(item.id))group.sources=group.sources.map(id=>id===item.id?value.id:id);
      else group.sources.push(value.id);
     }else group.sources=group.sources.filter(id=>id!==item.id);
    }
    await preview(next,(create?'Add ':'Update ')+kind+'/'+value.id,show,base,revision);
   },'primary')
  ]);
 }
 show();
}
async function toggle(kind,item,key='enabled'){
 const next=clone(S.config),entry=next[kind].find(x=>x.id===item.id);
 entry[key]=!entry[key];await preview(next,(entry[key]?'Enable ':'Disable ')+kind+'/'+item.id+(key==='auto_approved'?' for Auto':'')); 
}
async function remove(kind,item){
 const next=clone(S.config);next[kind]=next[kind].filter(x=>x.id!==item.id);
 await preview(next,'Delete '+kind+'/'+item.id);
}
function addAccount(provider=''){
 accountWizard({provider_id:provider});
}
function accountWizard(draft={}){
 const provider=select([...new Set([...S.catalog.map(p=>p.id),...(S.config.providers||[]).map(p=>p.id)])],draft.provider_id||'',true);
 const id=h('input',{value:draft.id||''}),name=h('input',{value:draft.display_name||''}),quota=h('input',{value:draft.quota_domain||''});
 const credential=select(S.credentials.map(c=>({value:c.id,label:c.id+' · '+c.kind})),draft.credential_ref||'',true);
 const available=[...(S.config.browser_profiles||[]),...(draft.newProfile?[draft.newProfile]:[])];
 const profile=select(available.map(p=>({value:p.id,label:p.id+(p===draft.newProfile?' (new isolated profile)':'')})),draft.browser_profile_id||'',true);
 const hints=h('p',{class:'muted'});
 const authNotice=h('p',{},draft.authenticated?'Login detected for this selected profile. Review and save the account.':'Choose a protected credential or a browser profile. Import actions return here with the new reference.');
 const capture=()=>({...draft,id:id.value.trim(),display_name:name.value.trim(),quota_domain:quota.value.trim(),provider_id:provider.value,credential_ref:credential.value,browser_profile_id:profile.value,newProfile:draft.newProfile?.id===profile.value?draft.newProfile:undefined});
 const hint=()=>{const p=S.catalog.find(p=>p.id===provider.value),d=S.descriptors.find(d=>d.id===p?.adapter);hints.textContent='Credential types: '+(d?.credential_modes||[]).join(', ')+'. Password imports are login material, not API keys. Accounts are saved disabled and excluded from Auto.'};provider.onchange=()=>{draft.authenticated=false;authNotice.textContent='Selection changed. Check login again for this selection.';hint()};profile.onchange=()=>{draft.authenticated=false;authNotice.textContent='Selection changed. Check login again for this selection.'};hint();
 const imported=saved=>{const next=capture(),items=Array.isArray(saved)?saved:[saved];if(items.length===1)next.credential_ref=items[0].id;accountWizard(next)};
 dialog('Set up account',[h('div',{class:'form-grid'},field('Provider',provider),field('ID',id),field('Display Name',name),field('Quota domain',quota),field('Credential',credential),field('Browser Profile Id',profile)),hints,authNotice,h('div',{class:'toolbar'},button('New credential',()=>credentialForm(undefined,imported)),button('Import password manager',()=>importCredentials(imported)),button('Import token',()=>importToken(imported)),button('Import browser cookies',()=>importBrowserCookies(imported)),button('New isolated profile',()=>newWizardProfile(capture())),button('Login with selected profile',async()=>{
  const next=capture(),selected=available.find(p=>p.id===next.browser_profile_id);if(!selected||!next.provider_id)throw new Error('Choose a provider and browser profile first.');
  const payload={profile:selected,provider:next.provider_id,action:'launch'};
  const check=()=>loginEvidence({id:next.id},true,'Finish signing in to the selected provider. The account has not been saved yet.',{path:'/admin/browser_profiles/setup-login',payload:{...payload,action:'check'},authenticated:()=>accountWizard({...next,authenticated:true}),back:()=>accountWizard(next)});
  try{await api('/admin/browser_profiles/setup-login',{method:'POST',body:JSON.stringify(payload)});check()}catch(e){if(!e.message.includes('port is already in use'))throw e;dialog('Browser port occupied',h('p',{},'Confirm that the selected connection belongs to the intended browser profile before checking login.'),[button('Back',()=>accountWizard(next)),button('Use running browser',check)])}
 }))],[button('Review changes',async()=>{
  const next=clone(S.config),value=capture();
  if(!value.id||!value.provider_id||!value.quota_domain)throw new Error('Enter account ID, provider and quota domain.');
  if((next.accounts||[]).some(a=>a.id===value.id))throw new Error('This account ID already exists.');
  if(value.newProfile){next.browser_profiles=next.browser_profiles||[];next.browser_profiles.push(value.newProfile)}
  next.providers=next.providers||[];if(!next.providers.some(p=>p.id===value.provider_id))next.providers.push({id:value.provider_id,enabled:true,auto_approved:false,pool_strategy:'round-robin'});
  next.accounts=next.accounts||[];next.accounts.push({id:value.id,provider_id:value.provider_id,display_name:value.display_name,quota_domain:value.quota_domain,credential_ref:value.credential_ref,browser_profile_id:value.browser_profile_id,enabled:false,auto_approved:false,max_inflight:1,weight:1,created_at:new Date().toISOString()});
  await preview(next,'Add account '+value.id,()=>accountWizard(value),clone(S.config),S.revision,value.authenticated?async()=>{await api('/admin/accounts/'+encodeURIComponent(value.id)+'/check-login',{method:'POST'});await refresh()}:undefined);
 },'primary')]);
}
function newWizardProfile(draft){
 const id=h('input',{value:draft.id?draft.id+'-browser':''}),engine=select(['chrome','edge','chromium'],'chrome');
 const used=new Set((S.config.browser_profiles||[]).map(p=>new URL(p.cdp_url).port));let port=9223;while(used.has(String(port)))port++;
 const endpoint=h('input',{value:'http://127.0.0.1:'+port});
 dialog('New isolated profile',[field('Profile ID',id),field('Browser engine',engine),field('Browser connection URL',endpoint),h('p',{class:'muted'},'A dedicated data directory is used. The profile and account are saved together after review. A launched browser stays open if you cancel setup.')],[button('Use profile',()=>accountWizard({...draft,newProfile:{id:id.value.trim(),engine:engine.value,cdp_url:endpoint.value,enabled:true},browser_profile_id:id.value.trim(),authenticated:false}),'primary')]);
}
function addSource(provider=''){
 const choice=select(S.catalog.filter(p=>p.implementation!=='not_implemented').map(p=>p.id),provider,true);
 const model=h('input',{placeholder:'Actual upstream model ID'});
 const base=h('input',{placeholder:'Optional endpoint override'});
 dialog('Add a source',h('div',{class:'form-grid'},field('Provider',choice),field('Model',model),field('Base URL',base)),[
  button('Continue',async()=>{
   const source=await api('/admin/preset?provider='+encodeURIComponent(choice.value)+'&model='+encodeURIComponent(model.value)+'&base_url='+encodeURIComponent(base.value));
   edit('sources',source,true);
  },'primary')
 ]);
}
function credentialForm(existing,onSaved){
 const id=h('input',{value:existing?.id?.replace('cred://','')||'',disabled:!!existing,placeholder:'credential-id'});
 const kind=select(['api_key','oauth','cookie','browser_session','username_password','cli_session','device_session','browser_profile'],existing?.kind||'api_key');
 const value=h('input',{type:'password',autocomplete:'new-password',placeholder:'New secret value'});
 const username=h('input',{autocomplete:'off',placeholder:'Username (username/password only)'});
 dialog(existing?'Replace credential':'Add credential',[h('div',{class:'form-grid'},field('Credential ID',id),field('Type',kind),field('Username',username),field('Secret value',value)),h('p',{class:'muted'},'The existing secret is never sent to this page. Saving replaces the protected value.')],[
  button('Save credential',async()=>{
   if(!existing&&S.credentials.some(c=>c.id==='cred://'+id.value))throw new Error('This credential ID exists. Use Replace from Credentials to change it.');
   const secret=kind.value==='username_password'?JSON.stringify({username:username.value,password:value.value}):value.value;
   const saved=await api('/admin/credentials/'+encodeURIComponent(id.value),{method:'PUT',body:JSON.stringify({kind:kind.value,source:'manual',value:secret})});
   value.value='';$('dialog').close();await refresh();
   onSaved?.(saved);
  },'primary')
 ]);
}
function deleteCredential(credential){
 dialog('Delete credential',h('p',{},'Delete '+credential.id+'? Accounts and sources must be unbound first.'),[
  button('Cancel',()=> $('dialog').close()),button('Delete credential',async()=>{await api('/admin/credentials/'+encodeURIComponent(credential.id.replace('cred://','')),{method:'DELETE'});$('dialog').close();await refresh()},'danger')
 ]);
}
async function providerDetail(provider){
 await refresh();
 const configured=(S.config.providers||[]).find(p=>p.id===provider.id);
 const descriptor=S.descriptors.find(d=>d.id===provider.adapter);
 const accounts=(S.config.accounts||[]).filter(a=>a.provider_id===provider.id);
 const sources=S.config.sources.filter(s=>s.provider===provider.id),sourceIDs=new Set(sources.map(s=>s.id));
 const runtime=S.status.sources.filter(s=>sourceIDs.has(s.id)),evidence=(S.status.verification||[]).filter(v=>sourceIDs.has(v.source));
 const verified=new Set(evidence.filter(v=>v.models.some(m=>m.status==='verified')).map(v=>v.source));
 const completed=runtime.reduce((n,s)=>n+s.completed,0),failed=runtime.reduce((n,s)=>n+s.failures,0);
 const group=select(S.config.groups.map(g=>g.id),'auto'),protocol=select(provider.protocols||[],'chat'),explanations=h('div',{});
 const explain=button('Explain provider eligibility',async()=>{
  const result=await api('/admin/routing/simulate',{method:'POST',body:JSON.stringify({model:group.value,protocol:protocol.value,bytes:100})});
  explanations.replaceChildren(table(['Source / model','Eligibility'],Object.entries(result).filter(([id])=>sources.some(s=>id.startsWith(s.id+'/'))).map(([id,reason])=>[id,reason]),'No configured models for this provider.'));
 });
 const rows=[['Type',provider.kind],['Adapter',provider.adapter],['Protocols',(provider.protocols||[]).join(', ')],['Implementation',provider.implementation],['Credential types',(descriptor?.credential_modes||[]).join(', ')],['Browser login check',descriptor?.browser_auth_check?'Supported':'Not implemented'],['Upstream verification',provider.live_verified?'Catalog contains live evidence':'Not live verified']];
 dialog(provider.id,[
  h('dl',{class:'key-value'},rows.flatMap(([k,v])=>[h('dt',{},k),h('dd',{},v)])),
  h('p',{class:'muted'},provider.notes||''),
  h('h3',{},'Configured runtime'),table(['Measure','Value'],[['Provider enabled',configured?(configured.enabled?'Yes':'No'):'Not configured'],['Provider Auto approval',configured?(configured.auto_approved?'Yes':'No'):'Not configured'],['Accounts / sources / models',accounts.length+' / '+sources.length+' / '+sources.reduce((n,s)=>n+s.models.length,0)],['Sources with matching generation evidence',verified.size+' / '+sources.length],['Successful / failed requests',completed+' / '+failed],['Observed success rate',completed+failed?(completed*100/(completed+failed)).toFixed(1)+'%':'No completed requests']]),
  h('p',{class:'muted'},'A source counts as verified when at least one configured model/protocol has matching generation evidence. This does not verify every model, capability, account or future request. Runtime counters include explicit checks; shared quota is not a sum of source capacities.'),
  h('h3',{},'Accounts'),table(['Account','Enabled / Auto','Credential','Browser authentication','In flight / limit','Actions'],accounts.map(a=>{
   const capacity=(S.status.accounts||[]).find(c=>c.id===a.id);
   return [a.display_name||a.id,(a.enabled?'Yes':'No')+' / '+(a.auto_approved?'Yes':'No'),a.credential_ref||'Not bound',accountAuth(a),(capacity?.active||0)+' / '+a.max_inflight,[button('Edit account',()=>edit('accounts',a)),a.browser_profile_id?button('Check login',()=>loginEvidence(a)):null]];
  }),'No accounts. Add an account to keep credentials and routing policy together.'),
  h('h3',{},'Sources'),table(['Source','Account','Routing state','Models','Matching generation evidence','Actions'],sources.map(s=>[s.id,s.account_id||'No account',state(s),s.models.length,verified.has(s.id)?'At least one model/protocol':'Not established',[button('Source details',()=>sourceDetail(s)),button('Validate source',()=>validateSource(s)),button('Discover models',()=>discoverModels(s))]]),'No sources configured.'),
  h('h3',{},'Provider routing eligibility'),h('p',{class:'muted'},'Read-only simulation for a 100-byte text request. Results explain eligibility, not final candidate selection.'),field('Provider group',group),field('Provider protocol',protocol),explain,explanations
 ],[
  button('Provider settings',()=>edit('providers',configured||{id:provider.id,enabled:true,auto_approved:false,pool_strategy:'round-robin'},!configured)),
  button('Add account',()=>addAccount(provider.id)),button('Add source',()=>addSource(provider.id),'primary')
 ]);
}
function overview(){
 const c=S.config,status=S.status,sources=c.sources||[],accounts=c.accounts||[];
 const flow=h('div',{class:'flow'},[
  ['Providers',S.catalog.length,(c.providers||[]).length+' configured'],
  ['Accounts',accounts.length,accounts.filter(a=>a.enabled).length+' enabled'],
  ['Sources',sources.length,status.sources.filter(s=>s.enabled).length+' enabled'],
  ['In flight',status.workload?.active??0,'Global source leases, including finishing old configurations']
 ].map(([label,n,sub])=>h('div',{class:'flow-cell'},h('span',{},label),h('strong',{},n),h('p',{},sub))));
 const attention=status.sources.filter(s=>s.blocked||s.failures||Date.parse(s.cooldown)>Date.now());
 return [pageHead('Overview','A direct view of your provider pool.',button('Add account',()=>addAccount()),button('Add source',()=>addSource(),'primary')),flow,h('div',{class:'split'},
  h('section',{class:'panel'},h('h2',{},'Needs attention'),attention.length?attention.map(s=>h('div',{class:'stat-line'},s.id,s.blocked?badge('Authentication / policy','bad'):badge(s.failures+' failures','warn'))):h('p',{class:'muted'},'No recorded failures. Untested sources still need verification.')),
  h('section',{class:'panel'},h('h2',{},'Routing groups'),c.groups.map(g=>h('div',{class:'stat-line'},h('a',{href:'#groups'},g.id),g.sources.length+' sources',badge(g.type))))
 ),h('section',{class:'panel'},h('h2',{},'Workload and memory'),workloadSummary(status)),h('section',{class:'panel'},h('h2',{},'Current gateway'),h('dl',{class:'key-value'},h('dt',{},'Listen'),h('dd',{},c.listen),h('dt',{},'Configuration'),h('dd',{},'Revision '+S.revision),h('dt',{},'Sources with matching validation'),h('dd',{},status.live_verified_sources||0),h('dt',{},'Requests / rejected'),h('dd',{},status.requests+' / '+status.rejected),h('dt',{},'Restart pending'),h('dd',{},S.restart.join(', ')||'No')))];
}
function workloadSummary(s){
 const mib=value=>(Number(value||0)/1048576).toFixed(2)+' MiB',w=s.workload||{};
 return [h('p',{class:'muted'},'Live snapshots. Queue counts the current routing generation; active leases include finishing old generations. Reserved buffers are application accounting. Go memory is not total process RSS. Average request rate covers the gateway lifetime, not a recent interval.'),table(['Measure','Value'],[
  ['Global in flight / limit',(w.active??0)+' / '+(w.active_limit??0)],['Routing queued / limit',(w.queued??0)+' / '+(w.queue_limit??0)],['Ingress occupied / limit',(s.ingress_active??0)+' / '+(s.ingress_limit??0)],['Reserved buffers / limit',mib(s.buffered_bytes)+' / '+mib(s.buffered_limit_bytes)],['Go heap allocated',mib(s.go_heap_bytes)],['Go runtime memory obtained',mib(s.go_runtime_bytes)],['Goroutines',s.goroutines??0],['Gateway uptime',Math.floor(s.uptime_seconds||0)+' seconds'],['Lifetime average request rate',Number(s.average_requests_per_second||0).toFixed(3)+' requests/s']
 ])];
}
function providers(){
 const query=h('input',{type:'search',placeholder:'Filter providers','aria-label':'Filter providers'});
 const type=select(['all',...new Set(S.catalog.map(p=>p.kind))],'all');
 const target=h('div',{});
 function draw(){
  const entries=S.catalog.filter(p=>(type.value==='all'||p.kind===type.value)&&p.id.toLowerCase().includes(query.value.toLowerCase()));
  target.replaceChildren(table(['Provider','Type','Protocols','Accounts','Implementation','State / actions'],entries.map(p=>{
   const configured=(S.config.providers||[]).find(x=>x.id===p.id);
   return [button(p.id,()=>providerDetail(p)),badge(p.kind),(p.protocols||[]).join(', '),(S.config.accounts||[]).filter(a=>a.provider_id===p.id).length,p.implementation,[configured?button(configured.enabled?'Disable':'Enable',()=>toggle('providers',configured)):badge('Not configured'),button('Open',()=>providerDetail(p))]];
  })));
 }
 query.oninput=draw;type.onchange=draw;draw();
 return [pageHead('Providers','Browse the catalog, then configure accounts and sources.',button('Add source',()=>addSource(),'primary')),h('div',{class:'toolbar'},query,type),target];
}
function accountAuth(a){
 const last=[...(S.evidence||[])].reverse().find(e=>e.kind==='authentication'&&e.resource===a.id);
 if(!last)return 'Not checked';
 return last.status+' · '+new Date(last.checked_at).toLocaleString()+(last.revision===S.revision?'':' · historical configuration');
}
function loginEvidence(a,watch=false,launchMessage='',setup){
 let live=true,busy=false,timer,deadline,controller;
 const progress=h('p',{role:'status'},watch?'Checking every 5 seconds after each result, for up to 5 minutes.':'Checking browser session…');
 const details=h('div',{});
 const stop=()=>{watch=false;clearTimeout(timer);clearTimeout(deadline);controller?.abort();if(live)progress.textContent='Checks stopped. Use Check now to run another check.'};
 const run=async()=>{
  if(!live||busy)return;busy=true;controller=new AbortController();
  try{
   const result=await api(setup?.path||'/admin/accounts/'+encodeURIComponent(a.id)+'/check-login',{method:'POST',signal:controller.signal,...(setup?{body:JSON.stringify(setup.payload)}:{})});
   if(!live)return;
   details.replaceChildren(table(['Check','Result'],[['Status',result.status],['Profile',result.profile],['Checked at',result.checked_at],['Configuration revision',result.revision],['Method',result.method],['Composer ready',result.composer_ready?'Yes':'Not established'],['Generation verified','No'],['History saved',result.history_recorded?'Yes':'No']]));
   if(['authenticated','unsupported','rate_limited'].includes(result.status)){watch=false;clearTimeout(deadline)}
   if(result.status==='authenticated'&&setup?.authenticated){setup.authenticated();return}
   progress.textContent=result.status==='authenticated'?'Browser session authenticated. Validate a configured source separately.':result.status==='unsupported'?'This provider does not yet implement a browser login check.':watch?'Waiting for you to finish login in the browser…':'Check complete.';
   S.evidence=await api('/admin/evidence',{signal:controller.signal});if(live)render();
  }catch(e){if(live&&e.name!=='AbortError'){watch=false;clearTimeout(deadline);progress.textContent=e.message}}
  finally{busy=false;if(live&&watch)timer=setTimeout(run,5000)}
 };
 dialog('Login evidence',[h('p',{},launchMessage||'Checks do not submit a prompt. Browser authentication and source generation are separate.'),progress,details],[button('Check now',run),button('Wait for login',()=>{watch=true;clearTimeout(timer);clearTimeout(deadline);deadline=setTimeout(stop,300000);return run()}),button('Stop checks',stop),setup?button('Back to account setup',setup.back):button('Open sources',()=>{$('dialog').close();location.hash='sources'})]);
 dialogCleanup=()=>{live=false;stop()};
 if(watch)deadline=setTimeout(stop,300000);
 run();
}
function accounts(){
 return [pageHead('Accounts','Account switches and capacity apply across their sources.',button('Account pools',accountPools),button('Refresh capacity',refresh),button('Add account',()=>addAccount(),'primary')),table(['Account','Provider','Credential','Browser authentication','Quota / in flight','Auto','Actions'],(S.config.accounts||[]).map(a=>[
  a.display_name||a.id,a.provider_id,a.credential_ref||'Not bound',accountAuth(a),a.quota_domain+' · '+((S.status.accounts||[]).find(x=>x.id===a.id)?.active||0)+' / '+a.max_inflight,badge(a.auto_approved?'Approved':'Manual',a.auto_approved?'accent':''),
  [button(a.enabled?'Disable':'Enable',()=>toggle('accounts',a)),button('Edit',()=>edit('accounts',a)),a.browser_profile_id?button('Login',async()=>{const result=await api('/admin/accounts/'+encodeURIComponent(a.id)+'/login',{method:'POST'});loginEvidence(a,true,result.message)}):null,a.browser_profile_id?button('Check login',()=>loginEvidence(a)):null,button('Delete',()=>remove('accounts',a),'danger')]
 ]),'No accounts. Add an account and bind a credential before enabling its sources.')];
}
function importBrowserCookies(onSaved){
 const profile=select((S.config.browser_profiles||[]).filter(p=>p.enabled).map(p=>p.id),'',true);
 const provider=select(S.catalog.filter(p=>(p.credentials?.accepted||[]).includes('cookie')&&p.base_url.startsWith('https://')).map(p=>p.id),'',true);
 dialog('Import browser cookies',[field('Configured browser profile',profile),field('Provider',provider),h('p',{class:'muted'},'Reads cookies only for the selected provider URL through the configured debugging connection. It does not scan other sites or verify login. Some browser adapters use their bound profile instead of a cookie credential.')],[button('Preview cookies',async()=>{
  const payload={profile:profile.value,provider:provider.value},path='/admin/credentials/import-browser';
  const preview=await api(path,{method:'POST',body:JSON.stringify(payload)});
  dialog('Browser cookie preview',[h('p',{},preview.domain+': '+preview.count+' cookies'),h('p',{class:'muted'},preview.message)],[button('Save cookies',async()=>{
   if(!preview.count)throw new Error('No cookies available.');
   const saved=await api(path,{method:'POST',body:JSON.stringify({...payload,apply:true})});$('dialog').close();await refresh();message('Cookies saved. Bind the new reference from Accounts and check login separately.');onSaved?.(saved);
  },'primary')]);
 },'primary')]);
}
function accountPools(){
 dialog('Account pools',table(['Provider','Strategy','Accounts','Actions'],(S.config.providers||[]).map(p=>[p.id,p.pool_strategy||'round-robin',(S.config.accounts||[]).filter(a=>a.provider_id===p.id).length,button('Edit pool',()=>editPool(p))])));
}
function editPool(provider){
 const base=clone(S.config),revision=S.revision,strategy=select(['round-robin','least-load','sticky','weighted'],provider.pool_strategy||'round-robin');
 const rows=(base.accounts||[]).filter(a=>a.provider_id===provider.id).map(a=>({a,enabled:h('input',{type:'checkbox',checked:a.enabled,'aria-label':'Enable '+a.id}),weight:h('input',{type:'number',min:1,max:10000,value:a.weight,'aria-label':'Weight '+a.id}),limit:h('input',{type:'number',min:1,max:10000,value:a.max_inflight,'aria-label':'Concurrency '+a.id})}));
 dialog('Edit account pool',[field('Pool strategy',strategy),h('p',{class:'muted'},'Account capacity is shared across its sources. Explicit fallback/select order and weighted source groups take precedence over account-pool selection.'),table(['Account','Enabled','Weight','Concurrent requests','In flight'],rows.map(x=>[x.a.id,x.enabled,x.weight,x.limit,(S.status.accounts||[]).find(a=>a.id===x.a.id)?.active||0]))],[button('Review pool changes',()=>{
  const next=clone(base);next.providers.find(p=>p.id===provider.id).pool_strategy=strategy.value;
  for(const x of rows){const a=next.accounts.find(a=>a.id===x.a.id);Object.assign(a,{enabled:x.enabled.checked,weight:Number(x.weight.value),max_inflight:Number(x.limit.value)})}
  return preview(next,'Update account pool '+provider.id,()=>editPool(provider),base,revision);
 },'primary')]);
}
function editQuota(domain){
 const base=clone(S.config),revision=S.revision,limit=h('input',{type:'number',min:1,max:10000,value:domain.limit});
 dialog('Edit shared quota',[h('p',{},domain.id+' · '+domain.sources.length+' sources · '+domain.active+' requests currently in flight'),field('Shared concurrent requests',limit),h('p',{class:'muted'},'Updates every source in this domain in one transaction. Lowering the limit lets existing requests finish and restricts new requests until capacity is available.')],[button('Review quota changes',()=>{
  const next=clone(base);for(const source of next.sources){if(source.quota_domain===domain.id)source.quota_max_inflight=Number(limit.value)}
  return preview(next,'Update shared quota '+domain.id,()=>editQuota(domain),base,revision);
 },'primary')]);
}
function credentials(){
 return [pageHead('Credentials','Protected values are stored separately from configuration.',h('div',{},button('Import export',importCredentials),button('Import token',importToken),button('Import browser cookies',importBrowserCookies),button('Add credential',()=>credentialForm(),'primary'))),table(['Credential','Type','Used by','Imported from','Updated','Actions'],S.credentials.map(c=>[
  c.id,badge(c.kind),[...(S.config.accounts||[]).filter(a=>a.credential_ref===c.id).map(a=>a.id),...S.config.sources.filter(s=>s.credential_ref===c.id).map(s=>s.id)].join(', ')||'Unbound',c.source,new Date(c.updated_at).toLocaleString(),[button('Replace',()=>credentialForm(c)),button('Delete',()=>deleteCredential(c),'danger')]
 ]),'No credentials. Add a key or session, then bind its reference to an account.')];
}
function importCredentials(onSaved){
 const file=h('input',{type:'file',accept:'.csv,.json'});
 const formatChoice=select(['auto','bitwarden-json','bitwarden-csv','1password-csv','keepassxc-csv','protonpass-csv','dashlane-csv','nordpass-csv','apple-passwords-csv','google-passwords-csv'],'auto');
 dialog('Import selected export',[field('Export format',formatChoice),field('CSV or JSON file',file),h('p',{class:'muted'},'Auto accepts CSV columns name (optional), url, username, password, or a JSON array with those fields. Choose the matching manager format for its native export. Up to 4 MiB and 1,000 login candidates. Notes, TOTP, cards and password history are not imported.')],[button('Preview entries',async()=>{
  const chosen=file.files[0];if(!chosen)throw new Error('Choose an export file.');
  if(chosen.size>4*1024*1024)throw new Error('Export exceeds 4 MiB.');
  const format=formatChoice.value==='auto'?(chosen.name.toLowerCase().endsWith('.json')?'json':'csv'):formatChoice.value;
  let data=await chosen.text();
  const report=await api('/admin/credentials/import',{method:'POST',body:JSON.stringify({format,data})}),rows=report.items;
  const selected=new Set();
  dialog('Select credentials to import',[h('p',{class:'muted'},'Skipped '+report.skipped+' non-web or non-password records. Only checked entries will be saved. Existing credentials will not be replaced. Bind the new references from Accounts after importing.'),table(['Select','Name','Domain','Type','Provider match'],rows.map(row=>[h('input',{type:'checkbox','aria-label':'Import entry '+(row.index+1),onchange:e=>{if(e.target.checked)selected.add(row.index);else selected.delete(row.index)}}),row.name,row.domain,row.kind,(row.matches||[]).map(m=>m.provider+(m.compatible?' (compatible)':' (login or different credential required)')).join(', ')||'No exact domain match']))],[button('Cancel',()=>{data='';$('dialog').close()}),button('Import selected',async()=>{
   if(!selected.size)throw new Error('Select at least one entry.');
   const saved=await api('/admin/credentials/import',{method:'POST',body:JSON.stringify({format,data,selected:[...selected],apply:true})});
   data='';$('dialog').close();await refresh();message('Imported '+saved.length+' credentials. Bind their references from Accounts.');
   onSaved?.(saved);
  },'primary')]);
 },'primary')]);
}
function sources(){
 return [pageHead('Sources','A source binds an account to one or more model targets.',button('Add source',()=>addSource(),'primary')),table(['Source','Provider / account','Type','State','Models','Groups','Actions'],S.config.sources.map(s=>[
  button(s.id,()=>sourceDetail(s)),s.provider+(s.account_id?' / '+s.account_id:''),badge(s.source_kind||'Unspecified'),state(s),s.models.length,S.config.groups.filter(g=>g.sources.includes(s.id)).map(g=>g.id).join(', '),[button(s.enabled?'Disable':'Enable',()=>toggle('sources',s)),button('Edit',()=>edit('sources',s)),button('Discover models',()=>discoverModels(s)),button('Validate',()=>validateSource(s)),button('Verification',()=>sourceVerification(s)),button('Delete',()=>remove('sources',s),'danger')]
 ]),'No sources. Add a provider preset and enter the model available to your account.')];
}
async function sourceDetail(source){
 await refresh();source=S.config.sources.find(s=>s.id===source.id);if(!source)throw Error('Source no longer exists.');
 const runtime=S.status.sources.find(s=>s.id===source.id)||{},verification=(S.status.verification||[]).find(v=>v.source===source.id),account=(S.config.accounts||[]).find(a=>a.id===source.account_id);
 const quota=(S.status.quota_domains||[]).find(q=>q.id===source.quota_domain),pool=(S.status.accounts||[]).find(a=>a.id===source.account_id);
 const timestamp=value=>value&&Date.parse(value)>0?new Date(value).toLocaleString():'Not observed';
 const millis=value=>value>0?value.toFixed(1)+' ms':'Not measured';
 const finished=(runtime.completed||0)+(runtime.failures||0),explanations=h('div',{}),group=select(S.config.groups.map(g=>g.id),'auto'),protocol=select([...new Set(source.models.flatMap(m=>m.protocols))],'chat'),size=h('input',{type:'number',value:100,min:0});
 const explain=button('Explain eligibility',async()=>{const result=await api('/admin/routing/simulate',{method:'POST',body:JSON.stringify({model:group.value,protocol:protocol.value,bytes:Number(size.value)})});explanations.replaceChildren(table(['Model','Eligibility for this request'],Object.entries(result).filter(([id])=>id.startsWith(source.id+'/')).map(([id,reason])=>[id,reason])))});
 dialog('Source / '+source.id,[
  h('h3',{},'Configuration'),table(['Setting','Value'],[['Provider / adapter',source.provider+' / '+source.adapter],['Account',account?.display_name||source.account_id||'No account'],['Credential configuration',verification?.credential_state||'Not established'],['Source type',source.source_kind||'Unspecified'],['Source enabled',source.enabled?'Yes':'No'],['Source Auto approval',source.auto_approved?'Yes':'No'],['Runtime routing state',state(source)],['Source concurrency',(runtime.active||0)+' / '+source.max_inflight],['Account concurrency',pool?pool.active+' / '+pool.limit:'No account limit'],['Shared quota',source.quota_domain+' · '+(quota?quota.active+' / '+quota.limit:'Not observed')],['Groups',S.config.groups.filter(g=>g.sources.includes(source.id)).map(g=>g.id).join(', ')||'None']]),
  h('h3',{},'Models'),table(['Model / upstream','Protocols','Tier / basis','Tools / vision','Enabled / Auto','USD input / output per million'],source.models.map(m=>[m.id+' / '+m.upstream,m.protocols.join(', '),m.tier+' / '+(m.rating_basis||'Not rated'),m.tools+' / '+(m.vision?'Yes':'No'),(m.enabled!==false?'Yes':'No')+' / '+(m.auto_approved===false?'No':m.auto_approved===true?'Yes':'Inherit'),(m.input_usd_per_million??'Unknown')+' / '+(m.output_usd_per_million??'Unknown')])),
  h('h3',{},'Observed runtime'),h('p',{class:'muted'},'Counters include explicit validation requests. Observations are for this runtime and do not prove current login, quota balance or model capability.'),table(['Metric','Value'],[['Successful / failed requests',(runtime.completed||0)+' / '+(runtime.failures||0)],['Observed success rate',finished?((runtime.completed||0)*100/finished).toFixed(1)+'%':'No completed requests'],['Rolling request duration',millis(runtime.latency_ms)],['Rolling time to first output',millis(runtime.ttft_ms)],['Last success',timestamp(runtime.last_success)],['Last failure',timestamp(runtime.last_failure)],['Last upstream HTTP status',runtime.last_http_status||'Not observed'],['Cooldown until',Date.parse(runtime.cooldown)>Date.now()?timestamp(runtime.cooldown):'Not cooling down']]),
  h('h3',{},'Generation evidence'),table(['Model','Protocol','Latest evidence','Checked at'],(verification?.models||[]).map(m=>[m.model,m.protocol,m.status,timestamp(m.checked_at)])),
  h('h3',{},'Routing eligibility'),h('p',{class:'muted'},'Read-only simulation for a text request. Eligibility is separate from which candidate the router will select.'),field('Group to explain',group),field('Protocol to explain',protocol),field('Request bytes to explain',size),explain,explanations
 ],[button('Edit source',()=>edit('sources',source)),button('Validate source',()=>validateSource(source)),button('Benchmark',()=>benchmarkSource(source)),button('Verification details',()=>sourceVerification(source))]);
}
function benchmarkSource(source){
 const model=select(source.models.map(m=>m.id),source.models[0]?.id),protocol=h('select',{}),output=h('div',{});
 const populate=()=>protocol.replaceChildren(...(source.models.find(m=>m.id===model.value)?.protocols||[]).map(p=>h('option',{value:p},p)));model.onchange=populate;populate();
 let stopped=false,running=false,controller=null;const stop=()=>{stopped=true;controller?.abort()};
 const run=button('Run 3 samples',async()=>{
  if(running)return;if(!model.value||!protocol.value)throw Error('Choose a configured model and protocol.');
  running=true;stopped=false;run.disabled=true;model.disabled=true;protocol.disabled=true;const rows=[];
  try{for(let i=0;i<3&&!stopped;i++){
   controller=new AbortController();const start=performance.now();
   const result=await api('/admin/sources/'+encodeURIComponent(source.id)+'/validate',{method:'POST',signal:controller.signal,body:JSON.stringify({model:model.value,protocol:protocol.value})});
   if(stopped)break;rows.push([String(i+1),result.verified?'Verified':'Failed',Math.round(performance.now()-start)+' ms',result.history_recorded?'Saved':'Not saved']);
   output.replaceChildren(table(['Sample','Generation result','Admin round-trip duration','Evidence history'],rows));
   if(!result.verified)break;
  }}catch(error){if(!stopped)throw error}finally{running=false;run.disabled=false;model.disabled=false;protocol.disabled=false}
 },'primary');
 dialog('Benchmark / '+source.id,[field('Benchmark model',model),field('Benchmark protocol',protocol),h('p',{class:'warning'},'Sends up to three sequential “Reply with OK.” generation requests. Provider usage may be billed. Stops at the first unsuccessful result. Closing or stopping cancels the current request and prevents further samples; already incurred usage cannot be undone.'),h('p',{class:'muted'},'Duration includes admin request, queue and generation time; it is not TTFT. Three samples are a small observation, not a capacity or quality guarantee. Each completed check is saved separately when evidence storage succeeds.'),output],[run,button('Stop samples',stop)]);
 dialogCleanup=stop;
}
async function sourceVerification(source){
 S.status=await api('/admin/status');
 const v=(S.status.verification||[]).find(v=>v.source===source.id);
 if(!v)throw new Error('Source verification metadata is unavailable.');
 dialog('Source verification',[h('p',{class:'muted'},'Point-in-time evidence for this source and credential version. A successful check does not enable routing or guarantee future availability.'),table(['Layer','Evidence'],[['Catalog implementation',v.catalog_implemented?'Implemented':'Not established'],['Catalog live evidence',v.catalog_live_verified?'Recorded in catalog':'Not established'],['Credential configuration',v.credential_state],['Credential version',v.credential_version||'Legacy / external']]),table(['Model','Protocol','Latest check','Checked at','Configuration revision'],v.models.map(m=>[m.model,m.protocol,m.status,m.checked_at?new Date(m.checked_at).toLocaleString():'Not checked',m.revision||'—']))]);
}
async function discoverModels(source){
 const result=await api('/admin/sources/'+encodeURIComponent(source.id)+'/discover',{method:'POST'});
 dialog('Discovered models',[h('p',{class:'muted'},(result.complete?'Complete list':'Partial list: limit reached')+' · '+result.pages+' pages · '+result.checked_at+'. Listing does not verify generation, tools or quality.'+(result.history_recorded?' History saved.':' History could not be saved.')),table(['Upstream model','Action'],result.models.map(m=>[m.id,button('Configure model',()=>{
  const next=clone(source);if(next.models.some(x=>x.id===m.id||x.upstream===m.id))throw new Error('This model is already configured.');
  next.models.push({id:m.id,upstream:m.id,protocols:[],tier:'unrated',tools:'unknown',vision:false,max_input_bytes:65536,enabled:false,auto_approved:false});
  edit('sources',next);
 })]))]);
}
function validateSource(source){
 const model=h('select',{},source.models.map(m=>h('option',{value:m.id},m.id))),proto=h('select',{});
 function protocols(){proto.replaceChildren(...source.models.find(m=>m.id===model.value).protocols.map(p=>h('option',{value:p},p)))}
 model.onchange=protocols;protocols();
 dialog('Validate source',[field('Model to validate',model),field('Protocol to validate',proto),h('p',{class:'muted'},'Sends one generation request: “Reply with OK.” Provider usage may be billed. This can check a disabled source without enabling its routes. No automatic retry is performed.')],[button('Run validation',async()=>{
  const result=await api('/admin/sources/'+encodeURIComponent(source.id)+'/validate',{method:'POST',body:JSON.stringify({model:model.value,protocol:proto.value})});
  await refresh();
  dialog('Validation evidence',table(['Check','Result'],[['Source / model',result.source+' / '+result.model],['Verified',result.verified?'Yes':'No'],['Checked at',result.checked_at],['History saved',result.history_recorded?'Yes':'No: result was not persisted'],['Protocol complete',result.result.protocol_complete?'Yes':'No'],['Output observed',result.output_observed?'Yes':'No'],['Error',result.result.upstream_error||'None']]));
 },'primary')]);
}
function models(){
 const entries=S.config.sources.flatMap(s=>s.models.map(m=>({source:s,model:m,key:JSON.stringify([s.id,m.id])}))),selected=new Set(),query=h('input',{type:'search'}),source=select(['all',...S.config.sources.map(s=>s.id)],'all'),output=h('div',{}),count=h('p',{role:'status'});
 const filtered=()=>entries.filter(x=>(source.value==='all'||x.source.id===source.value)&&(x.model.id+' '+x.model.upstream+' '+x.source.provider).toLowerCase().includes(query.value.toLowerCase()));
 const editSelected=button('Edit selected models',()=>bulkModels(entries.filter(x=>selected.has(x.key))),'primary');
 const draw=()=>{
  count.textContent=selected.size+' models selected, including selections outside the current filter.';editSelected.disabled=!selected.size;
  output.replaceChildren(table(['Select','Model','Source','Upstream','Tier','Tools / vision','Enabled','Auto approval',''],filtered().map(x=>[
   h('input',{type:'checkbox','aria-label':'Select model '+x.source.id+'/'+x.model.id,checked:selected.has(x.key),onchange:e=>{if(e.target.checked)selected.add(x.key);else selected.delete(x.key);draw()}}),x.model.id,x.source.id,x.model.upstream,badge(x.model.tier),x.model.tools+' / '+(x.model.vision?'yes':'no'),x.model.enabled===false?'Disabled':x.model.enabled===true?'Enabled':'Inherit',x.model.auto_approved===false?'Not approved':x.model.auto_approved===true?'Approved':'Inherit',button('Edit source models',()=>edit('sources',x.source))
  ]),'No matching configured models.'));
 };query.oninput=draw;source.onchange=draw;draw();
 return [pageHead('Models','Model names, capabilities and Auto approval remain explicit.',editSelected),field('Filter models',query),field('Model source',source),h('div',{class:'toolbar'},button('Select filtered models',()=>{for(const x of filtered())selected.add(x.key);draw()}),button('Clear model selection',()=>{selected.clear();draw()})),count,output];
}
function bulkModels(entries){
 if(!entries.length)throw Error('Select at least one model.');
 const base=clone(S.config),revision=S.revision;
 const switchOptions=[{value:'keep',label:'Leave unchanged'},{value:'true',label:'Yes'},{value:'false',label:'No'},{value:'inherit',label:'Inherit'}];
 const enabled=select(switchOptions,'keep'),approved=select(switchOptions,'keep'),tier=select(['keep','unrated','bronze','silver','gold','platinum','diamond'],'keep'),basis=h('input',{disabled:true}),tools=select(['keep','unknown','none','native'],'keep'),vision=select(switchOptions.filter(x=>x.value!=='inherit'),'keep');
 tier.onchange=()=>{basis.disabled=['keep','unrated'].includes(tier.value)};
 const show=()=>dialog('Edit selected models',[
  h('p',{class:'muted'},'Changes apply to all listed models in one configuration transaction. Approval is a routing permission, not verification. Provider, account, source and group restrictions still apply; metered sources may incur charges once routed.'),
  table(['Source','Model'],entries.map(x=>[x.source.id,x.model.id])),field('Models enabled',enabled),field('Models Auto approval',approved),field('Models tier',tier),field('Models rating basis',basis),field('Models tools',tools),field('Models vision',vision)
 ],[button('Review model changes',async()=>{
  const next=clone(base),changes={};
  for(const [key,input] of [['enabled',enabled],['auto_approved',approved],['vision',vision]])if(input.value!=='keep')changes[key]=input.value==='inherit'?null:input.value==='true';
  if(tier.value!=='keep'){if(tier.value!=='unrated'&&!basis.value.trim())throw Error('A rated tier requires a rating basis.');changes.tier=tier.value;changes.rating_basis=tier.value==='unrated'?'':basis.value.trim()}
  if(tools.value!=='keep')changes.tools=tools.value;
  if(!Object.keys(changes).length)throw Error('Choose at least one change.');
  for(const entry of entries){const model=next.sources.find(s=>s.id===entry.source.id)?.models.find(m=>m.id===entry.model.id);if(!model)throw Error('A selected model no longer exists. Refresh and select again.');Object.assign(model,changes)}
  await preview(next,'Update '+entries.length+' selected models',show,base,revision);
 },'primary')]);show();
}
function groups(){
 return [pageHead('Groups','A source can belong to several groups. Fallback order is editable.',button('Add group',()=>edit('groups',{id:'',type:'auto',sources:[],min_tier:'silver',allow_unrated:false,allow_paid:false,local_only:false,require_tools:false},true),'primary')),table(['Group','Strategy','Minimum tier','Sources in order','Actions'],S.config.groups.map(g=>[
  g.id,badge(g.type),g.min_tier,g.sources.join(' → '),[button('Edit',()=>edit('groups',g)),button('Delete',()=>remove('groups',g),'danger')]
 ]))];
}
function routing(){
 const model=h('input',{value:'auto/silver'}),protocol=select(['chat','responses','messages','gemini'],'chat'),size=h('input',{type:'number',value:120000,min:0});
 const tools=h('input',{type:'checkbox'}),vision=h('input',{type:'checkbox'}),stateful=h('input',{type:'checkbox'});
 const output=h('div',{});
 return [pageHead('Routing','Simulate eligibility without sending an upstream request.'),h('section',{class:'panel'},h('div',{class:'form-grid'},field('Model or group',model),field('Protocol',protocol),field('Input bytes',size),h('div',{},h('label',{class:'boolean'},tools,'Requires tools'),h('label',{class:'boolean'},vision,'Requires vision'),h('label',{class:'boolean'},stateful,'Stateful request'))),h('div',{class:'toolbar'},button('Simulate',async()=>{
  const result=await api('/admin/routing/simulate',{method:'POST',body:JSON.stringify({model:model.value,protocol:protocol.value,bytes:Number(size.value),tools:tools.checked,vision:vision.checked,stateful:stateful.checked})});
  output.replaceChildren(table(['Source / model','Eligibility'],Object.entries(result).map(([id,reason])=>[id,badge(reason,reason==='eligible'?'good':'warn')])));
 },'primary'))),output,h('h2',{},'Routing policies'),table(['Group','Strategy',''],S.config.groups.map(g=>[g.id,g.type,button('Edit policy',()=>edit('groups',g))]))];
}
function health(){
 return [pageHead('Health','Recorded outcomes, cooldowns and account capacity.'),table(['Source','State','In flight','Success / failure','Average duration','Cooldown until'],S.config.sources.map(source=>{
  const s=S.status.sources.find(x=>x.id===source.id)||{};
  return [source.id,state(source),s.active||0,(s.completed||0)+' / '+(s.failures||0),s.latency_ms?Math.round(s.latency_ms)+' ms':'Not measured',Date.parse(s.cooldown)>Date.now()?new Date(s.cooldown).toLocaleString():'—'];
 })),h('h2',{},'Shared quota domains'),h('p',{class:'muted'},'Each domain is counted once, including requests finishing after a configuration change.'),table(['Domain','In flight / limit','Sources','Actions'],(S.status.quota_domains||[]).map(q=>[q.id+(q.retired?' (retiring)':''),q.active+' / '+q.limit,q.sources.join(', ')||'Finishing previous configuration',q.retired?null:button('Edit shared quota',()=>editQuota(q))]))];
}
function metrics(){
 const s=S.status;
 return [pageHead('Metrics','Gateway counters across live configuration revisions.'),h('section',{class:'panel'},workloadSummary(s)),h('section',{class:'panel'},[
  ['Requests',s.requests],['Rejected at gateway',s.rejected],['Stream errors',s.stream_errors],['Response bytes',s.output_bytes],['Reserved request bytes',s.buffered_bytes]
 ].map(([key,value])=>h('div',{class:'stat-line'},key,h('strong',{},Number(value||0).toLocaleString()))))];
}
function configuration(section='runtime'){
 const tabs=h('div',{class:'config-tabs'}),body=h('section',{class:'panel'});
 function draw(name){
  for(const b of tabs.children)b.classList.toggle('selected',b.dataset.name===name);
  const schema=schemaFor(name),form=formField(schema,S.config[name],name),base=clone(S.config),revision=S.revision;
  body.replaceChildren(form.element,h('div',{class:'toolbar'},button('Review changes',async()=>{const next=clone(base);next[name]=form.read();await preview(next,'Update '+name,()=>{$('dialog').close()},base,revision)},'primary')));
 }
 for(const schema of S.schema){if(schema.name==='schema_version')continue;const b=button(title(schema.name),()=>draw(schema.name));b.dataset.name=schema.name;tabs.append(b)}
 draw(section);
 return [pageHead('Configuration','Every configuration field is available as a form.',button('Version history',()=>historyDialog())),S.restart.length?h('div',{class:'warning'},'Restart required: '+S.restart.join(', ')):null,tabs,body];
}
function historyDialog(){
 dialog('Configuration history',table(['Revision','Time','Change',''],[...S.history].reverse().map(v=>[v.revision,new Date(v.created_at).toLocaleString(),v.summary,button('Compare / restore',()=>{
  dialog('Restore revision '+v.revision,[diffTable(diff(S.config,v.config)),h('p',{class:'muted'},'Restoring creates a new revision. Credentials are not copied or restored.')],[button('Back',historyDialog),button('Restore',async()=>{await api('/admin/config/rollback',{method:'POST',body:JSON.stringify({revision:S.revision,target_revision:v.revision})});$('dialog').close();await refresh()},'primary')]);
 })])));
}
function environment(type){
 const key=type==='browsers'?'browser':'device',form=formField(schemaFor(key),S.config[key]),base=clone(S.config),revision=S.revision;
 return [pageHead(labels[type],type==='browsers'?'Browser connection and session limits.':'Explicitly configured Android execution environment.'),h('section',{class:'panel'},form.element,h('div',{class:'toolbar'},button('Review settings',async()=>{const next=clone(base);next[key]=form.read();await preview(next,'Update '+key,null,base,revision)},'primary')))];
}
function browsers(){
 return [pageHead('Browsers','Dedicated profiles keep account browser state separate.',button('Discover profiles',async()=>{const rows=await api('/admin/browser_profiles/discover',{method:'POST'});dialog('Discovered browser profiles',[h('p',{class:'muted'},'Up to 256 profiles from standard browser metadata locations. Discovery does not read cookies, verify login or copy sessions. Use Add profile and Accounts → Login to create an isolated session.'),table(['Browser','Profile','Name','Location','Authentication'],rows.map(p=>[p.browser,p.profile,p.name,p.root,p.authentication]),'No readable profile metadata found.')])}),button('Check connections',async()=>{const rows=await api('/admin/browser_profiles/status',{method:'POST'});dialog('Browser connection status',table(['Profile','Connection','Browser','Pages','Authentication'],rows.map(p=>[p.profile,p.state,p.browser||'Unknown',p.pages,p.authentication]),'No configured profiles.'))}),button('Add profile',()=>edit('browser_profiles',{id:'',enabled:true,engine:'chrome',cdp_url:'http://127.0.0.1:9223'},true),'primary')),table(['Profile','Engine','CDP endpoint','Accounts','Actions'],(S.config.browser_profiles||[]).map(p=>[
  p.id,p.engine,p.cdp_url,(S.config.accounts||[]).filter(a=>a.browser_profile_id===p.id).map(a=>a.display_name||a.id).join(', ')||'Unbound',[button(p.enabled?'Disable':'Enable',()=>toggle('browser_profiles',p)),button('Edit',()=>edit('browser_profiles',p)),button('Delete',()=>remove('browser_profiles',p),'danger')]
 ]),'No profiles. Add a profile, bind it from Accounts, then use Login.'),...environment('browsers').slice(1)];
}
function importToken(onSaved){
 const mode=select(['environment','codex','gemini-cli','oauth'],'environment');
 const source=select(S.config.sources.filter(s=>s.key_env).map(s=>s.id),'',true);
 const kind=select(['api_key','oauth','cookie'],'api_key');
 const file=h('input',{type:'file',accept:'.json'});
 dialog('Import token',[field('Import from',mode),field('Configured source for environment import',source),field('Environment credential type',kind),field('CLI session JSON file',file),h('p',{class:'muted'},'Environment import reads only the selected source’s configured variable. CLI import copies the current access token only; refresh tokens and account IDs are not imported. Source account-ID configuration may still be required.')],[button('Preview token',async()=>{
  let payload,path;
  if(mode.value==='environment'){path='/admin/credentials/import-env';payload={source:source.value,kind:kind.value}}
  else {const chosen=file.files[0];if(!chosen||chosen.size>1024*1024)throw new Error('Choose a CLI JSON file up to 1 MiB.');path='/admin/credentials/import-cli';payload={format:mode.value,data:await chosen.text()}}
  const preview=await api(path,{method:'POST',body:JSON.stringify(payload)});
  dialog('Token import preview',[h('p',{},'Available: '+(preview.available?'Yes':'No')),h('p',{},'Type: '+preview.kind),h('p',{class:'muted'},preview.message||'Variable: '+preview.variable+'. The value is never returned to this page.')],[button('Save imported token',async()=>{
   if(!preview.available)throw new Error('The selected credential is unavailable.');
   const saved=await api(path,{method:'POST',body:JSON.stringify({...payload,apply:true})});payload=null;$('dialog').close();await refresh();message('Imported token saved. Bind its new reference from Accounts.');onSaved?.(saved);
  },'primary')]);
 },'primary')]);
}
function sessions(){
 const target=h('div',{},h('p',{},'Loading session metadata…'));
 async function load(){
  const result=await api('/admin/sessions');if(!target.isConnected)return;
  target.replaceChildren(h('p',{class:'muted'},'Metadata only. '+(result.truncated?'The list reached its 1,000-session limit. ':'')+'Sources without session management: '+(result.unsupported_sources.join(', ')||'None')+'. Failed reads: '+(result.failed_sources.join(', ')||'None')+'.'),table(['Session','Provider / account','Upstream conversation','Created','Last activity','State','Actions'],result.items.map(item=>[
   item.id,item.provider+' / '+(item.account||'Legacy account'),item.conversation||'Not established',item.created.startsWith('0001-')?'Unknown':new Date(item.created).toLocaleString(),new Date(item.updated).toLocaleString(),item.expired?'Expired':item.dirty?'Incomplete turn':'Active',['expire','clear'].map(action=>button(title(action),()=>{
    dialog(title(action)+' local session',h('p',{},action==='clear'?'Remove this local session reference? The upstream conversation stays on the provider.':'Expire this local session reference? Its next request starts a new conversation.'),[button('Cancel',()=> $('dialog').close()),button(title(action)+' session',async()=>{await api('/admin/sessions/'+encodeURIComponent(item.source)+'/'+encodeURIComponent(item.id)+'/'+action,{method:'POST'});$('dialog').close();await load()},'danger')]);
   }))
  ]),'No stored sessions.'));
 }
 setTimeout(()=>load().catch(e=>message(e.message,true)),0);
 return [pageHead('Sessions','Inspect and manage local conversation references.',button('Refresh sessions',load)),target];
}
function activity(){
 const events=[...(S.status.execution_events||[])].reverse(),source=select(['all',...new Set(events.map(e=>e.source))],'all'),outcome=select(['all','succeeded','failed','canceled'],'all'),eventTable=h('div',{});
 const draw=()=>eventTable.replaceChildren(table(['Sequence / time','Source / model','Protocol / purpose','Outcome','Release / upstream HTTP','Duration / first output','Error category'],events.filter(e=>(source.value==='all'||e.source===source.value)&&(outcome.value==='all'||e.outcome===outcome.value)).map(e=>[e.sequence+' / '+new Date(e.finished_at).toLocaleString(),e.source+' / '+e.model,e.protocol+' / '+(e.validation?'Validation':'Dispatch'),e.outcome,e.status+' / '+(e.execution?.upstream_status||'Not observed'),e.duration_ms.toFixed(1)+' ms / '+(e.ttft_ms>0?e.ttft_ms.toFixed(1)+' ms':'Not observed'),e.execution?.upstream_error||'None']),'No matching execution events.'));
 source.onchange=draw;outcome.onchange=draw;draw();
 return [pageHead('Activity','Runtime execution events, persisted checks and configuration history.',button('Refresh history',refresh)),h('h2',{},'Execution events'),h('p',{class:'muted'},'Latest 1,000 released execution attempts, newest first. Kept in memory across hot updates; cleared on process restart. Retries are separate attempts. Rejections before a source is acquired and administrative-only releases are not included. No prompts, response bodies, URLs or credentials are recorded.'),field('Event source',source),field('Event outcome',outcome),eventTable,h('h2',{},'Checks'),table(['Time','Kind','Source / model','Configuration revision','Status','Method'],[...(S.evidence||[])].reverse().map(v=>[new Date(v.checked_at).toLocaleString(),v.kind,v.resource+(v.model?' / '+v.model:''),v.revision+(v.revision===S.revision?' (current)':' (historical)'),v.status,v.method]),'No recorded checks.'),h('h2',{},'Configuration changes'),table(['Revision','Time','Change'],[...S.history].reverse().map(v=>[v.revision,new Date(v.created_at).toLocaleString(),v.summary]))];
}
function about(){
 return [pageHead('About','Clash of Tokens'),h('section',{class:'panel'},h('p',{},'A local gateway for model API sources and simulated providers.'),h('p',{},'Provider implementation, account credentials and live upstream verification are separate states. Catalog coverage does not establish live availability.'),h('p',{},'Changes are validated and written to a versioned configuration journal. Credentials are referenced, never included in that journal.'))];
}
function searchResults(query){
 const q=query.toLowerCase(),results=[];
 for(const p of S.catalog)if(p.id.toLowerCase().includes(q))results.push([p.id,'Provider',()=>providerDetail(p)]);
 for(const kind of ['accounts','sources','groups'])for(const item of S.config[kind]||[])if(JSON.stringify(item).toLowerCase().includes(q))results.push([item.display_name||item.id,title(kind),()=>edit(kind,item)]);
 for(const c of S.credentials)if((c.id+' '+c.kind).toLowerCase().includes(q))results.push([c.id,'Credential',()=>credentialForm(c)]);
 return [pageHead('Search',results.length+' matching entries'),h('div',{class:'search-results'},results.map(([name,type,action])=>h('div',{class:'search-result'},button(name,action),h('span',{},type))))];
}
function render(){
 const route=location.hash.slice(1).split('/')[0]||'overview';
 for(const a of $('navigation').querySelectorAll('a'))a.setAttribute('aria-current',a.hash==='#'+route?'page':'false');
 if(!S.config){$('view').replaceChildren(connectView());return}
 if($('search').value){$('view').replaceChildren(...searchResults($('search').value));return}
 const renderers={overview,providers,accounts,credentials,sources,models,groups,routing,health,metrics,config:configuration,logs:activity,about,browsers,devices:()=>environment('devices'),sessions};
 $('view').replaceChildren(...(renderers[route]||overview)().filter(x=>x!=null));
}
for(const [group,items] of pages){$('navigation').append(h('div',{class:'nav-group'},group),...items.map(id=>h('a',{href:'#'+id},labels[id])))}
$('access').onclick=()=>{if(!S.token){$('view').replaceChildren(connectView());return}dialog('Gateway connection',h('p',{},'Disconnect to clear the management key and loaded configuration from this tab.'),[button('Disconnect',()=>{Object.assign(S,{token:'',config:null,credentials:[],history:[],catalog:[],status:{sources:[]}});$('access').textContent='Connect';$('dialog').close();message('Disconnected.');render()})])};
$('refresh').onclick=()=>buttonAction(refresh);
$('search').oninput=render;
window.addEventListener('hashchange',()=>{$('search').value='';render()});
render();
