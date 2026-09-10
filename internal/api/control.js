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
function dialog(name,body,actions=[]){
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
async function preview(next,summary,back,base=clone(S.config),revision=S.revision){
 const changes=diff(base,next);
 if(!changes.length){message('No changes to apply.');return}
 const result=await api('/admin/config/preview',{method:'POST',body:JSON.stringify({revision,config:next})});
 const body=[h('p',{},summary),diffTable(changes)];
 if(result.restart_required.length)body.unshift(h('div',{class:'warning'},'Restart required for: '+result.restart_required.join(', ')+'. Other valid source and routing changes apply immediately.'));
 body.push(h('p',{class:'muted'},'Provider, account and source switches affect new requests. Active requests can finish.'));
 dialog('Review changes',body,[button('Back',back||(()=> $('dialog').close())),button('Apply changes',async()=>{
  await api('/admin/config',{method:'PATCH',body:JSON.stringify({revision,config:next,summary})});
  $('dialog').close();await refresh();message('Changes saved. Revision '+S.revision+'.');
 },'primary')]);
}
function schemaFor(name){return S.schema.find(x=>x.name===name)}
function blank(schema){
 if(schema.nullable)return null;
 if(schema.type==='object')return Object.fromEntries((schema.fields||[]).map(f=>[f.name,blank(f)]));
 if(schema.type==='array')return [];
 if(schema.type==='boolean')return false;
 if(schema.type==='integer')return 0;
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
 else input=h('input',{type:schema.type==='integer'?'number':'text',value:value??'',step:schema.type==='integer'?'1':null});
 return {element:field(title(label)+(schema.restart_required?' (restart required)':''),input),read:()=>{
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
 edit('accounts',{id:'',provider_id:provider,display_name:'',enabled:false,auto_approved:false,credential_ref:'',browser_profile_id:'',quota_domain:'',max_inflight:1,weight:1,created_at:new Date().toISOString()},true);
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
function credentialForm(existing){
 const id=h('input',{value:existing?.id?.replace('cred://','')||'',disabled:!!existing,placeholder:'credential-id'});
 const kind=select(['api_key','oauth','cookie','browser_session','username_password','cli_session','device_session','browser_profile'],existing?.kind||'api_key');
 const value=h('input',{type:'password',autocomplete:'new-password',placeholder:'New secret value'});
 const username=h('input',{autocomplete:'off',placeholder:'Username (username/password only)'});
 dialog(existing?'Replace credential':'Add credential',[h('div',{class:'form-grid'},field('Credential ID',id),field('Type',kind),field('Username',username),field('Secret value',value)),h('p',{class:'muted'},'The existing secret is never sent to this page. Saving replaces the protected value.')],[
  button('Save credential',async()=>{
   const secret=kind.value==='username_password'?JSON.stringify({username:username.value,password:value.value}):value.value;
   await api('/admin/credentials/'+encodeURIComponent(id.value),{method:'PUT',body:JSON.stringify({kind:kind.value,source:'manual',value:secret})});
   value.value='';$('dialog').close();await refresh();
  },'primary')
 ]);
}
function deleteCredential(credential){
 dialog('Delete credential',h('p',{},'Delete '+credential.id+'? Accounts and sources must be unbound first.'),[
  button('Cancel',()=> $('dialog').close()),button('Delete credential',async()=>{await api('/admin/credentials/'+encodeURIComponent(credential.id.replace('cred://','')),{method:'DELETE'});$('dialog').close();await refresh()},'danger')
 ]);
}
function providerDetail(provider){
 const configured=(S.config.providers||[]).find(p=>p.id===provider.id);
 const descriptor=S.descriptors.find(d=>d.id===provider.adapter);
 const accounts=(S.config.accounts||[]).filter(a=>a.provider_id===provider.id);
 const rows=[['Type',provider.kind],['Adapter',provider.adapter],['Protocols',(provider.protocols||[]).join(', ')],['Implementation',provider.implementation],['Credential types',(descriptor?.credential_modes||[]).join(', ')],['Upstream verification',provider.live_verified?'Catalog contains live evidence':'Not live verified']];
 dialog(provider.id,[
  h('dl',{class:'key-value'},rows.flatMap(([k,v])=>[h('dt',{},k),h('dd',{},v)])),
  h('p',{class:'muted'},provider.notes||''),
  table(['Account','Enabled','Credential'],accounts.map(a=>[a.display_name||a.id,a.enabled?'Yes':'No',a.credential_ref||'Not bound']),'No accounts. Add an account to keep credentials and routing policy together.')
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
  ['In flight',status.sources.reduce((n,s)=>n+s.active,0),'Requests using account capacity']
 ].map(([label,n,sub])=>h('div',{class:'flow-cell'},h('span',{},label),h('strong',{},n),h('p',{},sub))));
 const attention=status.sources.filter(s=>s.blocked||s.failures||Date.parse(s.cooldown)>Date.now());
 return [pageHead('Overview','A direct view of your provider pool.',button('Add account',()=>addAccount()),button('Add source',()=>addSource(),'primary')),flow,h('div',{class:'split'},
  h('section',{class:'panel'},h('h2',{},'Needs attention'),attention.length?attention.map(s=>h('div',{class:'stat-line'},s.id,s.blocked?badge('Authentication / policy','bad'):badge(s.failures+' failures','warn'))):h('p',{class:'muted'},'No recorded failures. Untested sources still need verification.')),
  h('section',{class:'panel'},h('h2',{},'Routing groups'),c.groups.map(g=>h('div',{class:'stat-line'},h('a',{href:'#groups'},g.id),g.sources.length+' sources',badge(g.type))))
 ),h('section',{class:'panel'},h('h2',{},'Current gateway'),h('dl',{class:'key-value'},h('dt',{},'Listen'),h('dd',{},c.listen),h('dt',{},'Configuration'),h('dd',{},'Revision '+S.revision),h('dt',{},'Requests / rejected'),h('dd',{},status.requests+' / '+status.rejected),h('dt',{},'Restart pending'),h('dd',{},S.restart.join(', ')||'No')))];
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
function accounts(){
 return [pageHead('Accounts','Account switches and capacity apply across their sources.',button('Add account',()=>addAccount(),'primary')),table(['Account','Provider','Credential','Quota / concurrency','Auto','Actions'],(S.config.accounts||[]).map(a=>[
  a.display_name||a.id,a.provider_id,a.credential_ref||'Not bound',a.quota_domain+' / '+a.max_inflight,badge(a.auto_approved?'Approved':'Manual',a.auto_approved?'accent':''),
  [button(a.enabled?'Disable':'Enable',()=>toggle('accounts',a)),button('Edit',()=>edit('accounts',a)),a.browser_profile_id?button('Login',async()=>{const result=await api('/admin/accounts/'+encodeURIComponent(a.id)+'/login',{method:'POST'});message(result.message)}):null,a.browser_profile_id?button('Check login',async()=>{const result=await api('/admin/accounts/'+encodeURIComponent(a.id)+'/check-login',{method:'POST'});dialog('Login evidence',table(['Check','Result'],[['Status',result.status],['Checked at',result.checked_at||'Not checked'],['Method',result.method||'Not supported'],['Composer ready',result.composer_ready?'Yes':'Not established'],['Generation verified','No']]))}):null,button('Delete',()=>remove('accounts',a),'danger')]
 ]),'No accounts. Add an account and bind a credential before enabling its sources.')];
}
function credentials(){
 return [pageHead('Credentials','Protected values are stored separately from configuration.',h('div',{},button('Import export',importCredentials),button('Add credential',()=>credentialForm(),'primary'))),table(['Credential','Type','Used by','Imported from','Updated','Actions'],S.credentials.map(c=>[
  c.id,badge(c.kind),[...(S.config.accounts||[]).filter(a=>a.credential_ref===c.id).map(a=>a.id),...S.config.sources.filter(s=>s.credential_ref===c.id).map(s=>s.id)].join(', ')||'Unbound',c.source,new Date(c.updated_at).toLocaleString(),[button('Replace',()=>credentialForm(c)),button('Delete',()=>deleteCredential(c),'danger')]
 ]),'No credentials. Add a key or session, then bind its reference to an account.')];
}
function importCredentials(){
 const file=h('input',{type:'file',accept:'.csv,.json'});
 dialog('Import selected export',[field('CSV or JSON file',file),h('p',{class:'muted'},'CSV columns: name (optional), url, username, password. JSON: an array with these same fields. Up to 4 MiB and 1,000 entries. Login details are stored as username/password credentials; they are not API tokens.')],[button('Preview entries',async()=>{
  const chosen=file.files[0];if(!chosen)throw new Error('Choose an export file.');
  if(chosen.size>4*1024*1024)throw new Error('Export exceeds 4 MiB.');
  const format=chosen.name.toLowerCase().endsWith('.json')?'json':'csv';
  let data=await chosen.text();
  const rows=await api('/admin/credentials/import',{method:'POST',body:JSON.stringify({format,data})});
  const selected=new Set();
  dialog('Select credentials to import',[h('p',{class:'muted'},'Only checked entries will be saved. Existing credentials will not be replaced. Bind the new references from Accounts after importing.'),table(['Select','Name','Domain','Type','Provider match'],rows.map(row=>[h('input',{type:'checkbox','aria-label':'Import entry '+(row.index+1),onchange:e=>{if(e.target.checked)selected.add(row.index);else selected.delete(row.index)}}),row.name,row.domain,row.kind,(row.matches||[]).map(m=>m.provider+(m.compatible?' (compatible)':' (login or different credential required)')).join(', ')||'No exact domain match']))],[button('Cancel',()=>{data='';$('dialog').close()}),button('Import selected',async()=>{
   if(!selected.size)throw new Error('Select at least one entry.');
   const saved=await api('/admin/credentials/import',{method:'POST',body:JSON.stringify({format,data,selected:[...selected],apply:true})});
   data='';$('dialog').close();await refresh();message('Imported '+saved.length+' credentials. Bind their references from Accounts.');
  },'primary')]);
 },'primary')]);
}
function sources(){
 return [pageHead('Sources','A source binds an account to one or more model targets.',button('Add source',()=>addSource(),'primary')),table(['Source','Provider / account','Type','State','Models','Groups','Actions'],S.config.sources.map(s=>[
  s.id,s.provider+(s.account_id?' / '+s.account_id:''),badge(s.source_kind||'Unspecified'),state(s),s.models.length,S.config.groups.filter(g=>g.sources.includes(s.id)).map(g=>g.id).join(', '),[button(s.enabled?'Disable':'Enable',()=>toggle('sources',s)),button('Edit',()=>edit('sources',s)),button('Discover models',()=>discoverModels(s)),button('Validate',()=>validateSource(s)),button('Delete',()=>remove('sources',s),'danger')]
 ]),'No sources. Add a provider preset and enter the model available to your account.')];
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
  dialog('Validation evidence',table(['Check','Result'],[['Source / model',result.source+' / '+result.model],['Verified',result.verified?'Yes':'No'],['Checked at',result.checked_at],['History saved',result.history_recorded?'Yes':'No: result was not persisted'],['Protocol complete',result.result.protocol_complete?'Yes':'No'],['Output observed',result.output_observed?'Yes':'No'],['Error',result.result.upstream_error||'None']]));
 },'primary')]);
}
function models(){
 return [pageHead('Models','Model names, capabilities and Auto approval remain explicit.'),table(['Model','Source','Upstream','Tier','Tools / vision','State',''],S.config.sources.flatMap(s=>s.models.map(m=>[
  m.id,s.id,m.upstream,badge(m.tier),m.tools+' / '+(m.vision?'yes':'no'),badge(m.enabled===false?'Disabled':'Enabled',m.enabled===false?'':'good'),button('Edit source models',()=>edit('sources',s))
 ])))];
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
 }))];
}
function metrics(){
 const s=S.status;
 return [pageHead('Metrics','Gateway counters across live configuration revisions.'),h('section',{class:'panel'},[
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
 return [pageHead('Browsers','Dedicated profiles keep account browser state separate.',button('Add profile',()=>edit('browser_profiles',{id:'',enabled:true,engine:'chrome',cdp_url:'http://127.0.0.1:9223'},true),'primary')),table(['Profile','Engine','CDP endpoint','Accounts','Actions'],(S.config.browser_profiles||[]).map(p=>[
  p.id,p.engine,p.cdp_url,(S.config.accounts||[]).filter(a=>a.browser_profile_id===p.id).map(a=>a.display_name||a.id).join(', ')||'Unbound',[button(p.enabled?'Disable':'Enable',()=>toggle('browser_profiles',p)),button('Edit',()=>edit('browser_profiles',p)),button('Delete',()=>remove('browser_profiles',p),'danger')]
 ]),'No profiles. Add a profile, bind it from Accounts, then use Login.'),...environment('browsers').slice(1)];
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
 return [pageHead('Activity','Configuration history and the latest 1,000 persisted check results.',button('Refresh history',refresh)),h('h2',{},'Checks'),table(['Time','Kind','Source / model','Configuration revision','Status','Method'],[...(S.evidence||[])].reverse().map(v=>[new Date(v.checked_at).toLocaleString(),v.kind,v.resource+(v.model?' / '+v.model:''),v.revision+(v.revision===S.revision?' (current)':' (historical)'),v.status,v.method]),'No recorded checks.'),h('h2',{},'Configuration changes'),table(['Revision','Time','Change'],[...S.history].reverse().map(v=>[v.revision,new Date(v.created_at).toLocaleString(),v.summary]))];
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
