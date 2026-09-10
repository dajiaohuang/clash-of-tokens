'use strict';
const $ = id => document.getElementById(id);
const S = {token:'', config:null, revision:0, catalog:[], descriptors:[], schema:[], credentials:[], status:{sources:[]}, history:[], restart:[]};
const pages = [
 ['Workspace',['overview','providers','accounts','credentials','sources','models']],
 ['Traffic',['groups','routing','health','metrics']],
 ['Environment',['browsers','devices','sessions']],
 ['System',['config','logs','implementation','about']]
];
const labels = {overview:'Overview',providers:'Providers',accounts:'Accounts',credentials:'Credentials',sources:'Sources',models:'Models',groups:'Groups',routing:'Routing',health:'Health',metrics:'Metrics',browsers:'Browsers',devices:'Devices',sessions:'Sessions',config:'Configuration',logs:'Activity',implementation:'Implementation',about:'About'};
const names = {id:'ID',provider_id:'Provider',account_id:'Account',credential_ref:'Credential',credential_type_override:'Reviewed credential type override',base_url:'Base URL',key_env:'Legacy credential environment variable',account_id_env:'Legacy account ID environment variable',organization:'Organization',auto_approved:'Allow Auto routing',allow_paid:'Legacy paid-source policy',allow_unknown_cost:'Allow unknown costs',max_inflight:'Concurrent requests',quota_max_inflight:'Shared quota concurrency',quota_domain:'Quota domain',max_input_bytes:'Maximum input bytes',cdp_url:'Browser connection URL',source_kind:'Source type',tools:'Tool capability',local:'Loopback / local transport',paid:'Legacy paid flag'};
const browserEngines = ['chrome','edge','brave','firefox','opera','vivaldi','chromium','arc'];
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
 const health=status?.health;
 const labels={healthy:'Healthy',disabled:'Disabled',blocked:'Blocked',cooldown:'Cooldown',exhausted:'Exhausted',broken:'Broken',degraded:'Degraded',untested:'Not tested'};
 const kinds={healthy:'good',blocked:'bad',broken:'bad',cooldown:'warn',exhausted:'warn',degraded:'warn'};
 if(health)return badge(labels[health]||health,kinds[health]||'');
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
 if(result.impact){
  const rows=(result.impact.groups||[]).map(row=>[row.group,row.protocol,row.before_eligible,row.after_eligible]);
  const changed=(key)=>{const values=result.impact[key]||[];return values.length?values.join(', '):'None'};
  body.push(h('h3',{},'Routing impact (read-only)'),table(['Group','Protocol','Eligible before','Eligible after'],rows,'No group eligibility counts changed.'),h('p',{class:'muted'},'Counts use a zero-byte text request and never contact an upstream. They show configuration impact, not final candidate selection or provider availability. Configured sources: '+result.impact.sources_before+' → '+result.impact.sources_after+' · accounts: '+result.impact.accounts_before+' → '+result.impact.accounts_after+' · providers: '+result.impact.providers_before+' → '+result.impact.providers_after+' · browser profiles: '+result.impact.browser_profiles_before+' → '+result.impact.browser_profiles_after+'.'),table(['Changed resource','IDs'],[['Sources',changed('sources_changed')],['Accounts',changed('accounts_changed')],['Groups',changed('groups_changed')],['Providers',changed('providers_changed')],['Browser profiles',changed('browser_profiles_changed')]]));
 }
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
function formField(schema,value,label=schema.name,context={}){
 if(schema.type==='object'){
  const form=objectForm(schema.fields,value||{},context);
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
   const entry=formField(schema.item,v,schema.item.type==='object'?((v&&v.id)||'New item'):'',context);
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
 if(schema.name==='credential_ref'){
  const credentials=context.credentialModes?.length?S.credentials.filter(c=>context.credentialModes.includes(c.kind)||c.id===value):S.credentials;
  choices=[...new Set([...credentials.map(c=>c.id),...(value?[value]:[])])];
 }
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
function objectForm(fields,value,context={}){
 const entries=fields.map(schema=>({schema,field:formField(schema,value[schema.name],schema.name,context)}));
 return {element:h('div',{class:'form-grid'},entries.map(x=>x.field.element)),read:()=>Object.fromEntries(entries.map(x=>[x.schema.name,x.field.read()]))};
}
function edit(kind,item,create=false){
 const base=clone(S.config),revision=S.revision;
  const descriptor=kind==='sources'&&item.adapter?S.descriptors.find(d=>d.id===item.adapter):null;
  const form=objectForm(schemaFor(kind).item.fields,item,{credentialModes:descriptor?.credential_modes});
  let quotaConfirmed=false;
 let membership;
 if(kind==='sources'){
  membership=h('div',{class:'array'},h('h3',{},'Group membership'));
  for(const g of base.groups){
   const input=h('input',{type:'checkbox',checked:g.sources.includes(item.id),'data-group':g.id});
   membership.append(h('label',{class:'boolean'},input,g.id));
  }
 }
 function show(){
  dialog((create?'Add ':'Edit ')+title(kind).replace(/s$/,'')+(create?'':' / '+item.id),[form.element,(kind==='accounts'||kind==='sources')&&h('p',{class:'warning'},'A credential type override bypasses provider compatibility checks. Use it only after reviewing the credential format and adapter contract; the credential value is never shown here.'),membership],[
   button('Cancel',()=> $('dialog').close()),
   button('Review changes',async()=>{
    const next=clone(base),value=form.read();
    next[kind]=next[kind]||[];
    if(create)next[kind].push(value);else next[kind][next[kind].findIndex(x=>x.id===item.id)]=value;
    if(kind==='accounts'&&value.quota_domain!==item.quota_domain){
     for(const source of next.sources||[])if(source.account_id===item.id)source.quota_domain=value.quota_domain;
    }
    if(kind==='sources'&&value.account_id){
     const account=(next.accounts||[]).find(a=>a.id===value.account_id),moved=value.account_id!==item.account_id;
     if(account){
      // A newly-created preset starts with the catalog endpoint. Treat that
      // untouched preset value like an omitted field so an account's custom
      // endpoint can become the source default; edits to an existing source
      // remain explicit overrides.
      if(account.base_url&&(!value.base_url||(create&&value.base_url===item.base_url)))value.base_url=account.base_url;
      if(!value.organization)value.organization=account.organization||'';
      if(!value.project)value.project=account.project||'';
     }
     if(moved&&account)value.quota_domain=account.quota_domain;
     else if(!moved&&value.quota_domain!==item.quota_domain&&account){
      account.quota_domain=value.quota_domain;
      for(const source of next.sources||[])if(source.account_id===account.id)source.quota_domain=value.quota_domain;
     }
    }
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
     const quotaChanged=!create&&((kind==='accounts'&&value.quota_domain!==item.quota_domain)||(kind==='sources'&&value.quota_domain!==item.quota_domain));
     if(quotaChanged&&!quotaConfirmed){
      const affectedAccounts=kind==='accounts'?[item]:value.account_id?next.accounts.filter(a=>a.id===value.account_id):[];
      const affectedSources=kind==='accounts'?next.sources.filter(s=>s.account_id===item.id):value.account_id?next.sources.filter(s=>s.account_id===value.account_id):next.sources.filter(s=>s.id===item.id);
      dialog('Review quota change',[h('p',{class:'warning'},'Changing the quota domain updates shared capacity for the affected account and sources. Existing requests finish under their retained generation.'),table(['Affected configuration','Entries'],[['Accounts',affectedAccounts.map(a=>a.display_name||a.id).join(', ')||'None'],['Sources',affectedSources.map(s=>s.id).join(', ')||'None'],['New quota domain',value.quota_domain]])],[button('Back',show),button('Review quota change',()=>{quotaConfirmed=true;preview(next,(create?'Add ':'Update ')+kind+'/'+value.id,show,base,revision)},'primary')]);
      return;
     }
     await preview(next,(create?'Add ':'Update ')+kind+'/'+value.id,show,base,revision);
   },'primary')
  ]);
 }
 show();
}
async function toggle(kind,item,key='enabled'){
 const next=clone(S.config),entry=next[kind].find(x=>x.id===item.id);
 if(!entry)throw Error('Resource is no longer configured. Refresh and try again.');
 const enabling=!entry[key];entry[key]=enabling;
 const apply=()=>preview(next,(enabling?'Enable ':'Disable ')+kind+'/'+item.id+(key==='auto_approved'?' for Auto':''));
 if(kind==='providers'&&key==='enabled'&&!enabling){
  const accounts=(S.config.accounts||[]).filter(a=>a.provider_id===item.id);
  const sources=(S.config.sources||[]).filter(s=>s.provider===item.id);
  dialog('Disable provider',[h('p',{class:'warning'},'Disable '+item.id+' for new routing requests? Existing requests can finish; all accounts and sources under this provider will become ineligible.'),table(['Affected configuration','Entries'],[['Accounts',accounts.map(a=>a.display_name||a.id).join(', ')||'None'],['Sources',sources.map(s=>s.id).join(', ')||'None']])],[button('Cancel',()=>$('dialog').close()),button('Review disable',apply,'danger')]);
  return;
 }
 await apply();
}
function removeProvider(provider){
 const base=clone(S.config),revision=S.revision;
 const accounts=(base.accounts||[]).filter(a=>a.provider_id===provider.id);
 const sources=(base.sources||[]).filter(s=>s.provider===provider.id);
 const next=clone(base);next.providers=(next.providers||[]).filter(p=>p.id!==provider.id);
 const review=()=>{if(accounts.length||sources.length)throw Error('Provider '+provider.id+' still has bound accounts or sources; migrate those references before deleting it.');return preview(next,'Delete providers/'+provider.id,()=>removeProvider(provider),base,revision)};
 dialog('Delete provider',[h('p',{class:'warning'},'Delete '+provider.id+' from the configured provider registry? Bound accounts and sources must be migrated first; existing requests can finish under their retained configuration.'),table(['Affected configuration','Entries'],[['Accounts',accounts.map(a=>a.display_name||a.id).join(', ')||'None'],['Sources',sources.map(s=>s.id).join(', ')||'None']])],[button('Cancel',()=>$('dialog').close()),button('Review deletion',review,'danger')]);
}
function removeAccount(account){
 const base=clone(S.config),revision=S.revision;
 const sources=(base.sources||[]).filter(s=>s.account_id===account.id);
 const next=clone(base);next.accounts=(next.accounts||[]).filter(a=>a.id!==account.id);
 const review=()=>{if(sources.length)throw Error('Account '+account.id+' still has bound sources; migrate those references before deleting it.');return preview(next,'Delete accounts/'+account.id,()=>removeAccount(account),base,revision)};
 dialog('Delete account',[h('p',{class:'warning'},'Delete '+(account.display_name||account.id)+' from the account registry? Bound sources must be migrated first; existing requests can finish under their retained configuration.'),table(['Affected configuration','Entries'],[['Sources',sources.map(s=>s.id).join(', ')||'None']])],[button('Cancel',()=>$('dialog').close()),button('Review deletion',review,'danger')]);
}
async function toggleAuto(kind,item){
 const next=clone(S.config),entry=next[kind].find(x=>x.id===item.id);
 if(!entry)throw Error('Resource is no longer configured. Refresh and try again.');
 const enabling=!entry.auto_approved;entry.auto_approved=enabling;
 const paid=kind==='sources'&&entry.billing_mode==='metered' ||
   kind==='providers'&&S.config.sources.some(s=>s.provider===entry.id&&s.billing_mode==='metered') ||
   kind==='accounts'&&S.config.sources.some(s=>s.account_id===entry.id&&s.billing_mode==='metered');
 const notice=paid&&enabling?' This may incur additional charges because a metered source can become eligible for Auto routing.':'';
 await preview(next,(enabling?'Allow ':'Remove ')+kind+'/'+item.id+' for Auto.'+notice);
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
 const id=h('input',{value:draft.id||''}),name=h('input',{value:draft.display_name||''}),quota=h('input',{value:draft.quota_domain||''}),baseURL=h('input',{value:draft.base_url||'',placeholder:'Optional HTTPS endpoint override'}),organization=h('input',{value:draft.organization||'',placeholder:'Optional'}),project=h('input',{value:draft.project||'',placeholder:'Optional'});
 const providerDescriptor=()=>{const p=S.catalog.find(p=>p.id===provider.value),d=S.descriptors.find(d=>d.id===p?.adapter);return d};
 const allowedModes=()=>{const d=providerDescriptor();return d?.credential_modes?.length?d.credential_modes:null};
 const supports=(...modes)=>{const allowed=allowedModes();return !allowed||modes.some(mode=>allowed.includes(mode))};
 const compatibleCredentials=()=>{const allowed=allowedModes();return S.credentials.filter(c=>!allowed||allowed.includes(c.kind))};
 const credential=select(compatibleCredentials().map(c=>({value:c.id,label:c.id+' · '+c.kind})),draft.credential_ref||'',true);
 const available=[...(S.config.browser_profiles||[]),...(draft.newProfile?[draft.newProfile]:[])];
 const profile=select(available.map(p=>({value:p.id,label:p.id+(p===draft.newProfile?' (new isolated profile)':'')})),draft.browser_profile_id||'',true);
 const hints=h('p',{class:'muted'});
 const authNotice=h('p',{},draft.authenticated?'Login detected for this selected profile. Review and save the account.':'Choose a protected credential or a browser profile. Import actions return here with the new reference.');
 const capture=()=>({...draft,id:id.value.trim(),display_name:name.value.trim(),quota_domain:quota.value.trim(),provider_id:provider.value,credential_ref:credential.value,browser_profile_id:profile.value,base_url:baseURL.value.trim(),organization:organization.value.trim(),project:project.value.trim(),newProfile:draft.newProfile?.id===profile.value?draft.newProfile:undefined});
 const hint=()=>{const modes=allowedModes()||[];hints.textContent='Compatible credential types: '+(modes.join(', ')||'any declared type')+'. Incompatible protected references are hidden. Password imports are login material, not API keys. Base URL, organization and project are optional account defaults; a source may override them. Accounts are saved disabled and excluded from Auto routing.'};
 const updateCredentialOptions=()=>{const current=credential.value,options=compatibleCredentials();credential.replaceChildren(h('option',{value:''},'Choose…'),...options.map(c=>h('option',{value:c.id},c.id+' · '+c.kind)));credential.value=options.some(c=>c.id===current)?current:''};
 const newCredentialAction=button('New credential',()=>credentialForm(undefined,imported,allowedModes,providerDescriptor));
 const passwordImportAction=button('Import password manager',()=>importCredentials(imported));
 const tokenImportAction=button('Import token',()=>importToken(imported,allowedModes));
 const browserCookieAction=button('Import browser cookies',()=>importBrowserCookies(imported));
 const newProfileAction=button('New isolated profile',()=>newWizardProfile(capture()));
 const loginAction=button('Login with selected profile',async()=>{
  const next=capture(),selected=available.find(p=>p.id===next.browser_profile_id);if(!selected||!next.provider_id)throw new Error('Choose a provider and browser profile first.');
  const payload={profile:selected,provider:next.provider_id,action:'launch'};
  const check=()=>loginEvidence({id:next.id},true,'Finish signing in to the selected provider. The account has not been saved yet.',{path:'/admin/browser_profiles/setup-login',payload:{...payload,action:'check'},authenticated:()=>accountWizard({...next,authenticated:true}),back:()=>accountWizard(next)});
  try{await api('/admin/browser_profiles/setup-login',{method:'POST',body:JSON.stringify(payload)});check()}catch(e){if(!e.message.includes('port is already in use'))throw e;dialog('Browser port occupied',h('p',{},'Confirm that the selected connection belongs to the intended browser profile before checking login.'),[button('Back',()=>accountWizard(next)),button('Use running browser',check)])}
 });
 const setupActions=[newCredentialAction,passwordImportAction,tokenImportAction,browserCookieAction,newProfileAction,loginAction];
 const updateSetupActions=()=>{const d=providerDescriptor(),browser=!!(d?.browser_required||d?.browser_auth_check||allowedModes()?.includes('browser_profile'));passwordImportAction.hidden=!supports('username_password');tokenImportAction.hidden=!supports('api_key','oauth');browserCookieAction.hidden=!(browser&&supports('cookie'));newProfileAction.hidden=!browser;loginAction.hidden=!browser;newCredentialAction.hidden=!!allowedModes()&&(!allowedModes().length||!d?.credential_fields?.length)};
 provider.onchange=()=>{draft.authenticated=false;updateCredentialOptions();authNotice.textContent='Selection changed. Check login again for this selection.';hint();updateSetupActions()};profile.onchange=()=>{draft.authenticated=false;authNotice.textContent='Selection changed. Check login again for this selection.'};hint();updateSetupActions();
 const imported=saved=>{const next=capture(),items=Array.isArray(saved)?saved:[saved];if(items.length===1)next.credential_ref=items[0].id;accountWizard(next)};
 dialog('Set up account',[h('div',{class:'form-grid'},field('Provider',provider),field('ID',id),field('Display Name',name),field('Quota domain',quota),field('Base URL',baseURL),field('Organization',organization),field('Project',project),field('Credential',credential),field('Browser Profile Id',profile)),hints,authNotice,h('div',{class:'toolbar'},setupActions)],[button('Review changes',async()=>{
  const next=clone(S.config),value=capture();
  if(!value.id||!value.provider_id||!value.quota_domain)throw new Error('Enter account ID, provider and quota domain.');
  if((next.accounts||[]).some(a=>a.id===value.id))throw new Error('This account ID already exists.');
  if(value.newProfile){next.browser_profiles=next.browser_profiles||[];next.browser_profiles.push(value.newProfile)}
  next.providers=next.providers||[];if(!next.providers.some(p=>p.id===value.provider_id))next.providers.push({id:value.provider_id,enabled:true,auto_approved:false,pool_strategy:'round-robin'});
  next.accounts=next.accounts||[];next.accounts.push({id:value.id,provider_id:value.provider_id,display_name:value.display_name,base_url:value.base_url,organization:value.organization,project:value.project,quota_domain:value.quota_domain,credential_ref:value.credential_ref,browser_profile_id:value.browser_profile_id,enabled:false,auto_approved:false,max_inflight:1,weight:1,created_at:new Date().toISOString()});
  await preview(next,'Add account '+value.id,()=>accountWizard(value),clone(S.config),S.revision,value.authenticated?async()=>{await api('/admin/accounts/'+encodeURIComponent(value.id)+'/check-login',{method:'POST'});await refresh()}:undefined);
 },'primary')]);
}
function newWizardProfile(draft){
 const id=h('input',{value:draft.id?draft.id+'-browser':''}),engine=select(browserEngines,'chrome');
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
function credentialForm(existing,onSaved,allowedModes,descriptor){
 const id=h('input',{value:existing?.id?.replace('cred://','')||'',disabled:!!existing,placeholder:'credential-id'});
 const allKinds=['api_key','oauth','cookie','browser_session','username_password','cli_session','device_session','browser_profile'];
 const kinds=allowedModes?.()||allKinds;
 const kind=select(kinds,existing?.kind||kinds[0]||'api_key');
 const value=h('input',{type:'password',autocomplete:'new-password',placeholder:'New secret value'});
 const username=h('input',{autocomplete:'off',placeholder:'Username (username/password only)'});
 const fields=descriptor?.()?.credential_fields||[],secretField=fields.find(f=>f.secret)||fields.find(f=>f.name==='value'),valueLabel=secretField?.label||'Secret value';
 const usernameField=field('Username',username),valueField=field('Credential value',value);
 const updateCredentialFields=()=>{
  usernameField.hidden=kind.value!=='username_password';
  valueField.querySelector('label').textContent=kind.value==='username_password'?'Password':valueLabel;
  valueField.hidden=kind.value==='device_session';
 };
 kind.addEventListener('change',updateCredentialFields);
 updateCredentialFields();
 const formBody=[h('div',{class:'form-grid'},field('Credential ID',id),field('Type',kind),usernameField,valueField),h('p',{class:'muted'},'The existing secret is never sent to this page. Saving replaces the protected value.')];
 let confirmed=false;
 const save=async()=>{
   if(!existing&&S.credentials.some(c=>c.id==='cred://'+id.value))throw new Error('This credential ID exists. Use Replace from Credentials to change it.');
   const secret=kind.value==='username_password'?JSON.stringify({username:username.value,password:value.value}):value.value;
   const saved=await api('/admin/credentials/'+encodeURIComponent(id.value),{method:'PUT',body:JSON.stringify({kind:kind.value,source:'manual',value:secret})});
   value.value='';$('dialog').close();await refresh();
   onSaved?.(saved);
 };
 const saveButton=button('Save credential',async()=>{
  if(existing&&!confirmed){
   const accountRows=(S.config.accounts||[]).filter(a=>a.credential_ref===existing.id).map(a=>({id:a.id,label:a.display_name||a.id}));
   const accounts=accountRows.map(a=>a.label);
   const sources=(S.config.sources||[]).filter(s=>s.credential_ref===existing.id||((!s.credential_ref)&&accountRows.some(a=>a.id===s.account_id))).map(s=>s.id);
   confirmed=true;
   dialog('Review credential replacement',[h('p',{class:'warning'},'Replace the protected value for '+existing.id+'? In-flight requests are unchanged; matching generation evidence becomes historical and future checks must establish the new credential.'),table(['Affected configuration','Entries'],[['Accounts',accounts.join(', ')||'None'],['Sources',sources.join(', ')||'None'],['Credential type',existing.kind+' → '+kind.value]])],[button('Back',()=>{confirmed=false;dialog(existing?'Replace credential':'Add credential',formBody,[saveButton])}),button('Replace credential',save,'danger')]);
   return;
  }
  await save();
 },'primary');
 dialog(existing?'Replace credential':'Add credential',formBody,[saveButton]);
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
 const providerHealth=(S.status.provider_health||[]).find(x=>x.id===provider.id)||{};
 const verified=new Set(evidence.filter(v=>v.models.some(m=>m.status==='verified')).map(v=>v.source));
 const completed=runtime.reduce((n,s)=>n+s.completed,0),failed=runtime.reduce((n,s)=>n+s.failures,0);
 const group=select(S.config.groups.map(g=>g.id),'auto'),protocol=select(provider.protocols||[],'chat'),explanations=h('div',{});
 const explain=button('Explain provider eligibility',async()=>{
  const result=await api('/admin/routing/simulate',{method:'POST',body:JSON.stringify({model:group.value,protocol:protocol.value,bytes:100})});
  explanations.replaceChildren(table(['Source / model','Eligibility'],Object.entries(result).filter(([id])=>sources.some(s=>id.startsWith(s.id+'/'))).map(([id,reason])=>[id,reason]),'No configured models for this provider.'));
 });
 const rows=[['Type',providerTypeLabel(provider)],['Adapter',provider.adapter],['Protocols',(provider.protocols||[]).join(', ')],['Implementation',provider.implementation],['Credential types',(descriptor?.credential_modes||[]).join(', ')],['Browser login check',descriptor?.browser_auth_check?'Supported':'Not implemented'],['Upstream verification',provider.live_verified?'Catalog contains live evidence':'Not live verified']];
 dialog(provider.id,[
  h('dl',{class:'key-value'},rows.flatMap(([k,v])=>[h('dt',{},k),h('dd',{},v)])),
  h('p',{class:'muted'},provider.notes||''),
  h('h3',{},'Configured runtime'),table(['Measure','Value'],[['Provider enabled',configured?(configured.enabled?'Yes':'No'):'Not configured'],['Provider Auto approval',configured?(configured.auto_approved?'Yes':'No'):'Not configured'],['Pool strategy',providerHealth.pool_strategy||configured?.pool_strategy||'round-robin'],['Provider health',providerHealth.health||'untested'],['Provider auth status',providerHealth.auth_status||'not_checked'],['Catalog implementation',providerHealth.catalog_implemented?(providerHealth.catalog_live_verified?'Live catalog':'Implemented catalog'):'Not implemented'],['Verified sources',String(providerHealth.verified_sources||0)+' / '+sources.length],['Last generation validation',providerHealth.last_validated_at?(new Date(providerHealth.last_validated_at).toLocaleString()+' · '+(providerHealth.last_validation_state||'recorded')):'Not checked'],['Last auth check',providerHealth.last_auth_checked_at?new Date(providerHealth.last_auth_checked_at).toLocaleString():'Not checked'],['Accounts / sources / models',accounts.length+' / '+sources.length+' / '+sources.reduce((n,s)=>n+s.models.length,0)],['Sources with matching generation evidence',verified.size+' / '+sources.length],['Successful / failed requests',completed+' / '+failed],['Observed success rate',completed+failed?(completed*100/(completed+failed)).toFixed(1)+'%':'No completed requests']]),
  h('p',{class:'muted'},'A source counts as verified when at least one configured model/protocol has matching generation evidence. This does not verify every model, capability, account or future request. Runtime counters include explicit checks; shared quota is not a sum of source capacities.'),
  h('h3',{},'Accounts'),table(['Account','Enabled / Auto','Credential','Credential state','Reviewed override','Browser authentication','In flight / limit','Actions'],accounts.map(a=>{
   const capacity=(S.status.accounts||[]).find(c=>c.id===a.id);
   const credentialState=capacity?.credential_state||'not_configured';
   const credentialVersion=capacity?.credential_version?(' · v'+capacity.credential_version):'';
   return [a.display_name||a.id,(a.enabled?'Yes':'No')+' / '+(a.auto_approved?'Yes':'No'),a.credential_ref||'Not bound',credentialState+credentialVersion,a.credential_type_override?'Yes':'No',accountAuth(a),(capacity?.active||0)+' / '+a.max_inflight,[button('Edit account',()=>edit('accounts',a)),a.browser_profile_id?button('Login',()=>launchAccountLogin(a)):null,a.browser_profile_id?button('Refresh session',()=>loginEvidence(a)):null,button('Delete account',()=>removeAccount(a),'danger')]];
  }),'No accounts. Add an account to keep credentials and routing policy together.'),
  h('h3',{},'Sources'),table(['Source','Account','Routing state','Models','Matching generation evidence','Actions'],sources.map(s=>[s.id,s.account_id||'No account',state(s),s.models.length,verified.has(s.id)?'At least one model/protocol':'Not established',[button('Source details',()=>sourceDetail(s)),button('Validate source',()=>validateSource(s)),button('Discover models',()=>discoverModels(s))]]),'No sources configured.'),
  h('h3',{},'Provider routing eligibility'),h('p',{class:'muted'},'Read-only simulation for a 100-byte text request. Results explain eligibility, not final candidate selection.'),field('Provider group',group),field('Provider protocol',protocol),explain,explanations
 ],[
   configured?button('Validate provider',()=>validateProvider(provider),'primary'):null,
   button('Provider settings',()=>edit('providers',configured||{id:provider.id,enabled:true,auto_approved:false,pool_strategy:'round-robin'},!configured)),
   configured?button('Delete provider',()=>removeProvider(configured),'danger'):null,
   button('Add account',()=>addAccount(provider.id)),button('Add source',()=>addSource(provider.id),'primary')
 ]);
}
function providerTypeLabel(provider){
 const descriptor=S.descriptors.find(d=>d.id===provider.adapter);
 if(provider.kind==='api')return 'Official API';
 if(provider.kind==='cloud')return 'Cloud API';
 if(provider.kind==='aggregator')return 'Aggregator';
 if(provider.kind==='device')return 'App reverse';
 if(provider.kind==='research')return 'Research';
 if(provider.kind==='local')return ['devin-cli','zcode'].includes(provider.adapter)?'CLI reverse':'Local';
 if(provider.kind==='subscription')return ['devin-cli','zcode','codex','claude-code','gemini-cli','qwen-code','kimi-code','kiro','antigravity','amazon-q'].includes(provider.adapter)?'CLI / subscription':'Subscription';
 if(provider.kind==='web'&&(descriptor?.browser_required||descriptor?.browser_auth_check))return 'Browser reverse';
 if(provider.kind==='web')return 'Web reverse';
 return provider.kind||'Custom';
}
const sourceTypeLabels={vendor_api:'Official API',cloud_api:'Cloud API',aggregator_api:'Aggregator',product_reverse:'Web reverse',browser_reverse:'Browser reverse',app_reverse:'App reverse',cli_reverse:'CLI reverse',local_model:'Local model',custom_api:'Custom API'};
const sourceTypeLabel=kind=>sourceTypeLabels[kind]||kind||'Unspecified';
async function validateProvider(provider){
 const result=await api('/admin/providers/'+encodeURIComponent(provider.id)+'/validate',{method:'POST'});
 await refresh();
 const checks=result.checks||{};
 const rows=[['Provider',provider.id],['Status',result.verified?'Verified':result.status||'Failed'],['Account',result.account||'Not reported'],['Source / model',result.source?(result.source+' / '+result.model):'Browser authentication'],['Protocol',result.protocol||'Not applicable'],['Checked at',result.checked_at||'Not reported'],['History saved',result.history_recorded?'Yes':'No'],['Connection',checks.connection||'Not reported'],['Authentication',checks.auth||result.status||'Not reported'],['Request',checks.request||'Not applicable'],['Streaming',checks.streaming||'Not applicable'],['Completion',checks.completion||'Not applicable'],['Latency',checks.duration_ms===undefined?'Not reported':Number(checks.duration_ms).toFixed(1)+' ms'],['Output observed',result.output_observed===undefined?'Not reported':result.output_observed?'Yes':'No'],['Error',result.result?.upstream_error||'None']];
 dialog('Provider validation',[h('p',{class:'muted'},'One explicit check uses the first configured source/model/protocol. Browser-only providers use the first account authentication check. This does not enable routing or prove every model, capability or account.'),table(['Check','Result'],rows)]);
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
 const type=select(['all',...new Set(S.catalog.map(providerTypeLabel))].sort((a,b)=>a==='all'?-1:b==='all'?1:a.localeCompare(b)),'all');
 const target=h('div',{});
 function draw(){
  const entries=S.catalog.filter(p=>(type.value==='all'||providerTypeLabel(p)===type.value)&&p.id.toLowerCase().includes(query.value.toLowerCase()));
  target.replaceChildren(table(['Provider','Type','Protocols','Accounts','Implementation','State / actions'],entries.map(p=>{
   const configured=(S.config.providers||[]).find(x=>x.id===p.id);
  return [button(p.id,()=>providerDetail(p)),badge(providerTypeLabel(p)),(p.protocols||[]).join(', '),(S.config.accounts||[]).filter(a=>a.provider_id===p.id).length,p.implementation,[configured?button(configured.enabled?'Disable':'Enable',()=>toggle('providers',configured)):badge('Not configured'),configured?button(configured.auto_approved?'Remove Auto':'Allow Auto',()=>toggleAuto('providers',configured)):null,configured?button('Validate',()=>validateProvider(p)):null,button('Open',()=>providerDetail(p))]];
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
async function launchAccountLogin(a){
 const result=await api('/admin/accounts/'+encodeURIComponent(a.id)+'/login',{method:'POST'});
 loginEvidence(a,true,result.message);
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
 const healthFor=a=>(S.status.account_health||[]).find(x=>x.id===a.id)||{};
 const validateAccount=async a=>{
  const result=await api('/admin/accounts/'+encodeURIComponent(a.id)+'/validate',{method:'POST'});
  const status=result.verified?'verified':(result.status||'failed');
  dialog('Account validation',[h('p',{class:'muted'},'One explicit check was performed using the first configured source/model, or browser authentication for a browser-only account. This does not approve Auto routing or prove every model capability.'),table(['Field','Value'],[['Account',a.display_name||a.id],['Status',status],['Source',result.source||'Browser profile'],['Model',result.model||'Not applicable'],['Protocol',result.protocol||'Not applicable'],['Output observed',result.output_observed===undefined?'Not reported':result.output_observed?'Yes':'No'],['History saved',result.history_recorded?'Yes':'No']])]);
  await refresh();
 };
 return [pageHead('Accounts','Account switches and capacity apply across their sources.',button('Discover candidates',discoverAccounts),button('Account pools',accountPools),button('Refresh capacity',refresh),button('Add account',()=>addAccount(),'primary')),table(['Account','Provider','Health','Auth status','Browser authentication','Credential','Credential state','Routing defaults','Reviewed override','Quota / in flight','Auto','Actions'],(S.config.accounts||[]).map(a=>[
  a.display_name||a.id,a.provider_id,badge(healthFor(a).health||'untested',healthFor(a).health==='healthy'?'good':healthFor(a).health==='disabled'?'':'warn'),healthFor(a).auth_status||'not_checked',accountAuth(a),a.credential_ref||'Not bound',(healthFor(a).credential_state||'not_configured')+(healthFor(a).credential_version?' · v'+healthFor(a).credential_version:''),[a.base_url&&'Base URL: '+a.base_url,a.organization&&'Organization: '+a.organization,a.project&&'Project: '+a.project].filter(Boolean).join(' · ')||'Not set',a.credential_type_override?'Yes':'No',a.quota_domain+' · '+((S.status.accounts||[]).find(x=>x.id===a.id)?.active||0)+' / '+a.max_inflight,badge(a.auto_approved?'Approved':'Manual',a.auto_approved?'accent':''),
   [button(a.enabled?'Disable':'Enable',()=>toggle('accounts',a)),button(a.auto_approved?'Remove Auto':'Allow Auto',()=>toggleAuto('accounts',a)),button('Edit',()=>edit('accounts',a)),button('Validate account',()=>validateAccount(a)),a.browser_profile_id?button('Re-authenticate',()=>launchAccountLogin(a)):null,a.browser_profile_id?button('Check login',()=>loginEvidence(a)):null,button('Delete',()=>removeAccount(a),'danger')]
 ]),'No accounts. Add an account and bind a credential before enabling its sources.')];
}
async function discoverAccounts(){
 const scan=h('input',{type:'checkbox','aria-label':'Scan installed browser profile metadata'});
 const run=async()=>{
  const result=await api('/admin/discovery/accounts',{method:'POST',body:JSON.stringify({scan_browsers:scan.checked})});
  const rows=(result.items||[]).map(x=>[x.label,badge(x.kind),x.origin,(x.providers||[]).join(', ')||'No exact catalog match',x.confidence,x.available?'Available':'Not available',x.action,['bind_credential','use_existing_profile'].includes(x.action)?button('Use in account',()=>{ $('dialog').close(); accountWizard({credential_ref:x.kind==='browser_profile'?'':x.id,browser_profile_id:x.kind==='browser_profile'?x.id.replace('profile:',''):'' ,provider_id:x.providers?.[0]||''}) }):x.action==='create_browser_profile'?button('Configure profile',()=>configureDiscoveredProfile(x)):null]);
  dialog('Account discovery',[h('p',{class:'muted'},'Candidates are metadata only. Secret values, cookies and passwords never leave the protected store. Browser scanning reads standard profile metadata only and does not launch a browser or test login.'),table(['Discovery channel','Status','Action','Read only'],(result.channels||[]).map(x=>[x.id,x.status,x.action,x.read_only?'Yes':'No'])),table(['Candidate','Type','Origin','Provider suggestions','Evidence','Availability','Next action','Binding'],rows,'No candidates found. Import a credential or configure a browser profile first.')],[button('Run again',run),button('Open credentials',()=>{$('dialog').close();location.hash='credentials'}),button('Open accounts',()=>{$('dialog').close();location.hash='accounts'})]);
 };
 dialog('Discover accounts',[field('Scan installed browser profile metadata',scan),h('p',{class:'muted'},'Stored credential references and configured environment names are included without exposing their values. Enable the scan only when you want to inspect local browser profile metadata.'),button('Discover',run,'primary')]);
}
function configureDiscoveredProfile(candidate){
 const engineName=browserEngines.includes(candidate.browser)?candidate.browser:'chrome';
 const existing=new Set((S.config.browser_profiles||[]).map(p=>p.id));
 let id=(candidate.browser+'-'+candidate.id.slice(0,10)).replace(/[^a-zA-Z0-9_-]/g,'-');
 if(existing.has(id))id+='-profile';
 const profileID=h('input',{value:id}),engine=select(browserEngines,engineName),endpoint=h('input',{value:S.config.browser?.cdp_url||'http://127.0.0.1:9222'});
 dialog('Configure discovered browser profile',[h('p',{class:'muted'},'Discovery found '+candidate.label+' at '+candidate.origin+'. It read profile metadata only. Enter the loopback CDP endpoint of a browser already running with this profile; no cookies are copied and this action does not launch a browser.'),h('div',{class:'form-grid'},field('Profile ID',profileID),field('Browser engine',engine),field('Browser connection URL',endpoint))],[button('Use in account',()=>{const value=profileID.value.trim();if(!value)throw Error('Profile ID is required.');$('dialog').close();accountWizard({provider_id:candidate.providers?.[0]||'',browser_profile_id:value,newProfile:{id:value,engine:engine.value,cdp_url:endpoint.value.trim(),enabled:true}})},'primary')]);
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
 dialog('Account pools',table(['Provider','Strategy','Accounts','Enabled','In flight / limit','Weights','Actions'],(S.config.providers||[]).map(p=>{
  const accounts=(S.config.accounts||[]).filter(a=>a.provider_id===p.id),health=accounts.map(a=>(S.status.account_health||[]).find(x=>x.id===a.id)||{}),active=health.reduce((n,a)=>n+(a.active||0),0),limit=accounts.reduce((n,a)=>n+(a.max_inflight||0),0),weights=accounts.map(a=>(a.display_name||a.id)+': '+(a.weight||1)).join(', ')||'None';
  return [p.id,p.pool_strategy||'round-robin',accounts.length,accounts.filter(a=>a.enabled).length,active+' / '+limit,weights,button('Edit pool',()=>editPool(p))];
 })));
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
 const base=clone(S.config),revision=S.revision,name=h('input',{value:domain.id}),limit=h('input',{type:'number',min:1,max:10000,value:domain.limit});
 const accounts=(base.accounts||[]).filter(a=>a.quota_domain===domain.id),sources=(base.sources||[]).filter(s=>s.quota_domain===domain.id);
 dialog('Edit shared quota',[h('p',{},domain.id+' · '+sources.length+' sources · '+domain.active+' requests currently in flight'),h('p',{class:'warning'},'This change affects '+accounts.length+' account'+(accounts.length===1?'':'s')+' and '+sources.length+' source'+(sources.length===1?'':'s')+'. Existing requests finish under their retained generation.'),table(['Affected configuration','Entries'],[['Accounts',accounts.map(a=>a.display_name||a.id).join(', ')||'None'],['Sources',sources.map(s=>s.id).join(', ')||'None']]),field('Quota domain',name),field('Shared concurrent requests',limit),h('p',{class:'muted'},'Renaming or changing the limit updates every account and source in this domain in one transaction. New requests use the new shared boundary.')],[button('Review quota changes',()=>{
  const next=clone(base),renamed=name.value.trim();if(!renamed)throw Error('Quota domain is required.');
  for(const account of next.accounts||[])if(account.quota_domain===domain.id)account.quota_domain=renamed;
  for(const source of next.sources){if(source.quota_domain===domain.id){source.quota_domain=renamed;source.quota_max_inflight=Number(limit.value)}}
  return preview(next,'Update shared quota '+domain.id,()=>editQuota(domain),base,revision);
 },'primary')]);
}
function unbindCredential(credential){
 const base=clone(S.config),revision=S.revision;
 const accounts=(base.accounts||[]).filter(a=>a.credential_ref===credential.id);
 const accountIDs=new Set(accounts.map(a=>a.id));
 const sources=(base.sources||[]).filter(s=>s.credential_ref===credential.id||(!s.credential_ref&&accountIDs.has(s.account_id)));
 if(!accounts.length&&!sources.length)throw Error('This credential is already unbound.');
 const next=clone(base);
 for(const account of next.accounts||[])if(account.credential_ref===credential.id)account.credential_ref='';
 for(const source of next.sources||[])if(source.credential_ref===credential.id)source.credential_ref='';
 dialog('Unbind credential',[h('p',{class:'warning'},'Remove '+credential.id+' from all accounts and sources? The protected value remains in the vault until you delete it.'),table(['Affected configuration','Entries'],[['Accounts',accounts.map(a=>a.display_name||a.id).join(', ')||'None'],['Sources',sources.map(s=>s.id).join(', ')||'None']])],[button('Cancel',()=>$('dialog').close()),button('Review unbind',()=>preview(next,'Unbind credential '+credential.id,()=>unbindCredential(credential),base,revision),'primary')]);
}
function reloginCredential(credential){
 const accounts=(S.config.accounts||[]).filter(a=>a.credential_ref===credential.id&&a.browser_profile_id);
 if(!accounts.length)throw Error('No browser-bound account uses '+credential.id+'.');
 dialog('Re-login account',h('p',{class:'muted'},'Choose a browser-bound account. The login browser is isolated to its configured profile; this does not reveal or export the credential value.'),accounts.map(a=>button(a.display_name||a.id,async()=>{await launchAccountLogin(a)})));
}
function credentials(){
 return [pageHead('Credentials','Protected values are stored separately from configuration.',h('div',{},button('Import export',importCredentials),button('Import token',importToken),button('Import browser cookies',importBrowserCookies),button('Add credential',()=>credentialForm(),'primary'))),table(['Credential','Type','Used by','Imported from','Updated','Last used','Actions'],S.credentials.map(c=>[
  c.id,badge(c.kind),(()=>{const accounts=(S.config.accounts||[]).filter(a=>a.credential_ref===c.id),ids=accounts.map(a=>a.id);return [...ids,...S.config.sources.filter(s=>s.credential_ref===c.id||(!s.credential_ref&&accounts.some(a=>a.id===s.account_id))).map(s=>s.id)].join(', ')||'Unbound'})(),c.source,new Date(c.updated_at).toLocaleString(),c.last_used_at?new Date(c.last_used_at).toLocaleString():'Not used', [button('Replace',()=>credentialForm(c)),(S.config.accounts||[]).some(a=>a.credential_ref===c.id&&a.browser_profile_id)?button('Re-login',()=>reloginCredential(c)):null,button('Unbind',()=>unbindCredential(c)),button('Delete',()=>deleteCredential(c),'danger')]
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
  button(s.id,()=>sourceDetail(s)),s.provider+(s.account_id?' / '+s.account_id:''),badge(sourceTypeLabel(s.source_kind)),state(s),s.models.length,S.config.groups.filter(g=>g.sources.includes(s.id)).map(g=>g.id).join(', '),[button(s.enabled?'Disable':'Enable',()=>toggle('sources',s)),button(s.auto_approved?'Remove Auto':'Allow Auto',()=>toggleAuto('sources',s)),button('Edit',()=>edit('sources',s)),button('Discover models',()=>discoverModels(s)),button('Validate',()=>validateSource(s)),button('Verification',()=>sourceVerification(s)),button('Delete',()=>remove('sources',s),'danger')]
 ]),'No sources. Add a provider preset and enter the model available to your account.')];
}
async function sourceDetail(source){
 await refresh();source=S.config.sources.find(s=>s.id===source.id);if(!source)throw Error('Source no longer exists.');
 const runtime=S.status.sources.find(s=>s.id===source.id)||{},verification=(S.status.verification||[]).find(v=>v.source===source.id),account=(S.config.accounts||[]).find(a=>a.id===source.account_id);
 const effective={...source};
 for(const key of ['base_url','organization','project'])if(!effective[key]&&account?.[key])effective[key]=account[key];
 const quota=(S.status.quota_domains||[]).find(q=>q.id===source.quota_domain),pool=(S.status.accounts||[]).find(a=>a.id===source.account_id);
 const timestamp=value=>value&&Date.parse(value)>0?new Date(value).toLocaleString():'Not observed';
 const millis=value=>value>0?value.toFixed(1)+' ms':'Not measured';
 const tokens=value=>Number(value||0).toLocaleString();
 const cost=runtime.cost_known?'$'+Number(runtime.estimated_cost_usd||0).toFixed(6):'Unknown (usage or declared rates missing)';
 const finished=(runtime.completed||0)+(runtime.failures||0),explanations=h('div',{}),group=select(S.config.groups.map(g=>g.id),'auto'),protocol=select([...new Set(source.models.flatMap(m=>m.protocols))],'chat'),size=h('input',{type:'number',value:100,min:0});
 const explain=button('Explain eligibility',async()=>{const result=await api('/admin/routing/simulate',{method:'POST',body:JSON.stringify({model:group.value,protocol:protocol.value,bytes:Number(size.value)})});explanations.replaceChildren(table(['Model','Eligibility for this request'],Object.entries(result).filter(([id])=>id.startsWith(source.id+'/')).map(([id,reason])=>[id,reason])))});
 dialog('Source / '+source.id,[
  h('h3',{},'Configuration'),table(['Setting','Value'],[['Provider / adapter',source.provider+' / '+source.adapter],['Base URL',effective.base_url||'Not configured'],['Organization',effective.organization||'Not configured'],['Project',effective.project||'Not configured'],['Account',account?.display_name||source.account_id||'No account'],['Credential configuration',verification?.credential_state||'Not established'],['Credential binding',verification?.credential_binding||'none'],['Credential reference',verification?.credential_ref||'Not exposed'],['Credential mode',source.credential_mode||'Not declared'],['Source type',sourceTypeLabel(source.source_kind)],['Execution location',source.execution_location||'Not declared'],['Inference location',source.inference_location||'Not declared'],['Billing mode',source.billing_mode||'Not declared'],['Source enabled',source.enabled?'Yes':'No'],['Source Auto approval',source.auto_approved?'Yes':'No'],['Runtime routing state',state(source)],['Source concurrency',(runtime.active||0)+' / '+source.max_inflight],['Account concurrency',pool?pool.active+' / '+pool.limit:'No account limit'],['Shared quota',source.quota_domain+' · '+(quota?quota.active+' / '+quota.limit:'Not observed')],['Groups',S.config.groups.filter(g=>g.sources.includes(source.id)).map(g=>g.id).join(', ')||'None']]),
  h('h3',{},'Models'),table(['Model / upstream','Protocols','Tier / basis','Tools / vision','Enabled / Auto','USD input / output per million'],source.models.map(m=>[m.id+' / '+m.upstream,m.protocols.join(', '),m.tier+' / '+(m.rating_basis||'Not rated'),m.tools+' / '+(m.vision?'Yes':'No'),(m.enabled!==false?'Yes':'No')+' / '+(m.auto_approved===false?'No':m.auto_approved===true?'Yes':'Inherit'),(m.input_usd_per_million??'Unknown')+' / '+(m.output_usd_per_million??'Unknown')])),
  h('h3',{},'Observed runtime'),h('p',{class:'muted'},'Counters include explicit validation requests. Observations are for this runtime and do not prove current login, quota balance or model capability.'),table(['Metric','Value'],[['Successful / failed requests',(runtime.completed||0)+' / '+(runtime.failures||0)],['Observed success rate',finished?((runtime.completed||0)*100/finished).toFixed(1)+'%':'No completed requests'],['Input tokens observed',tokens(runtime.input_tokens)],['Output tokens observed',tokens(runtime.output_tokens)],['Total tokens observed',tokens(runtime.total_tokens)],['Estimated declared cost',cost],['Rolling request duration',millis(runtime.latency_ms)],['Rolling time to first output',millis(runtime.ttft_ms)],['Last success',timestamp(runtime.last_success)],['Last failure',timestamp(runtime.last_failure)],['Last upstream HTTP status',runtime.last_http_status||'Not observed'],['Cooldown until',Date.parse(runtime.cooldown)>Date.now()?timestamp(runtime.cooldown):'Not cooling down']]),
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
 dialog('Source verification',[h('p',{class:'muted'},'Point-in-time evidence for this source and credential version. A successful check does not enable routing or guarantee future availability.'),table(['Layer','Evidence'],[['Catalog implementation',v.catalog_implemented?'Implemented':'Not established'],['Catalog live evidence',v.catalog_live_verified?'Recorded in catalog':'Not established'],['Credential configuration',v.credential_state],['Credential binding',v.credential_binding||'none'],['Credential reference',v.credential_ref||'Not exposed'],['Credential version',v.credential_version||'Legacy / external']]),table(['Model','Protocol','Latest check','Checked at','Configuration revision'],v.models.map(m=>[m.model,m.protocol,m.status,m.checked_at?new Date(m.checked_at).toLocaleString():'Not checked',m.revision||'—']))]);
}
async function discoverModels(source){
 const result=await api('/admin/sources/'+encodeURIComponent(source.id)+'/discover',{method:'POST'});
 dialog('Discovered models',[h('p',{class:'muted'},(result.complete?'Complete list':'Partial list: limit reached')+' · '+result.pages+' pages · '+result.checked_at+'. Metadata is provider-reported; discovery does not verify generation, tools, vision or quality.'+(result.history_recorded?' History saved.':' History could not be saved.')),table(['Model','Owner','Token limits','Generation methods','Created','Action'],result.models.map(m=>[m.display_name&&m.display_name!==m.id?m.display_name+' ('+m.id+')':m.id,m.owned_by||'Not reported',m.input_token_limit||m.output_token_limit?(m.input_token_limit||'—')+' / '+(m.output_token_limit||'—'):'Not reported',(m.supported_methods||[]).join(', ')||'Not reported',m.created_unix?new Date(m.created_unix*1000).toLocaleString():'Not reported',button('Configure model',()=>{
  const next=clone(source);if(next.models.some(x=>x.id===m.id||x.upstream===m.id))throw new Error('This model is already configured.');
  next.models.push({id:m.id,upstream:m.id,declared_model:m.id,canonical_model:m.id,protocols:[],tier:'unrated',tools:'unknown',vision:false,max_input_bytes:65536,enabled:false,auto_approved:false});
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
  const checks=result.checks||{};
  const rows=[['Source / model',result.source+' / '+result.model],['Verified',result.verified?'Yes':'No'],['Checked at',result.checked_at],['History saved',result.history_recorded?'Yes':'No: result was not persisted'],['Connection',checks.connection||'Not reported'],['Authentication',checks.auth||'Not reported'],['Request',checks.request||'Not reported'],['Streaming',checks.streaming||'Not reported'],['Completion',checks.completion||'Not reported'],['Latency',checks.duration_ms===undefined?'Not reported':Number(checks.duration_ms).toFixed(1)+' ms'],['Output observed',result.output_observed?'Yes':'No'],['Error',result.result.upstream_error||'None']];
  dialog('Validation evidence',h('div',{},h('p',{class:'muted'},'One explicit request; no automatic retry.'),table(['Check','Result'],rows)));
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
 const enabled=select(switchOptions,'keep'),approved=select(switchOptions,'keep'),tier=select(['keep','unrated','bronze','silver','gold','platinum','diamond'],'keep'),basis=h('input',{disabled:true}),tools=select(['keep','unknown','none','native'],'keep'),vision=select(switchOptions.filter(x=>x.value!=='inherit'),'keep'),group=select([{value:'keep',label:'Leave unchanged'},...(S.config.groups||[]).map(g=>({value:g.id,label:g.id}))],'keep');
 tier.onchange=()=>{basis.disabled=['keep','unrated'].includes(tier.value)};
 const show=()=>dialog('Edit selected models',[
  h('p',{class:'muted'},'Changes apply to all listed models in one configuration transaction. Approval is a routing permission, not verification. Provider, account, source and group restrictions still apply; metered sources may incur charges once routed.'),
  table(['Source','Model'],entries.map(x=>[x.source.id,x.model.id])),field('Models enabled',enabled),field('Models Auto approval',approved),field('Models tier',tier),field('Models rating basis',basis),field('Models tools',tools),field('Models vision',vision),field('Add selected sources to group',group)
 ],[button('Review model changes',async()=>{
  const next=clone(base),changes={};
  for(const [key,input] of [['enabled',enabled],['auto_approved',approved],['vision',vision]])if(input.value!=='keep')changes[key]=input.value==='inherit'?null:input.value==='true';
  if(tier.value!=='keep'){if(tier.value!=='unrated'&&!basis.value.trim())throw Error('A rated tier requires a rating basis.');changes.tier=tier.value;changes.rating_basis=tier.value==='unrated'?'':basis.value.trim()}
  if(tools.value!=='keep')changes.tools=tools.value;
  if(!Object.keys(changes).length&&!group.value)throw Error('Choose at least one change.');
  for(const entry of entries){const model=next.sources.find(s=>s.id===entry.source.id)?.models.find(m=>m.id===entry.model.id);if(!model)throw Error('A selected model no longer exists. Refresh and select again.');Object.assign(model,changes)}
  if(group.value&&group.value!=='keep'){
   const target=next.groups.find(g=>g.id===group.value);if(!target)throw Error('Selected group no longer exists. Refresh and select again.');
   for(const id of new Set(entries.map(x=>x.source.id)))if(!target.sources.includes(id))target.sources.push(id);
  }
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
  const result=await api('/admin/routing/simulate',{method:'POST',body:JSON.stringify({model:model.value,protocol:protocol.value,bytes:Number(size.value),tools:tools.checked,vision:vision.checked,stateful:stateful.checked,detail:true})});
  const candidates=result.candidates||[];
  output.replaceChildren(h('p',{class:'muted'},result.selected?'Selected target: '+result.selected:'No target selected; capacity or eligibility rules prevented dispatch.'),table(['Order','Source / model','Eligibility','Selection'],candidates.map(candidate=>[candidate.order,candidate.id,badge(candidate.reason,candidate.eligible?'good':'warn'),candidate.selected?'Selected':''])));
 },'primary'))),output,h('h2',{},'Routing policies'),table(['Group','Strategy',''],S.config.groups.map(g=>[g.id,g.type,button('Edit policy',()=>edit('groups',g))]))];
}
function health(){
 const accountRows=(S.config.accounts||[]).map(account=>{const row=(S.status.account_health||[]).find(x=>x.id===account.id)||{};return [account.id,account.provider_id,row.enabled?'Yes':'No',row.auto_approved?'Yes':'No',row.pool_strategy||'round-robin',row.weight||account.weight||'—',row.credential_state||'not_configured'+(row.credential_version?' · v'+row.credential_version:''),row.credential_binding||'none',row.credential_ref||'Not exposed',row.credential_type_override?'Yes':'No',badge(row.health||'untested',row.health==='healthy'?'good':row.health==='disabled'?'':'warn'),row.auth_status||'not_checked',(row.active||0)+' / '+(row.limit||account.max_inflight),row.sources?.join(', ')||'None',(row.completed||0)+' / '+(row.failures||0),row.last_validated_at?(new Date(row.last_validated_at).toLocaleString()+' · '+(row.last_validation_state||'recorded')):'Not checked',row.last_auth_checked_at?new Date(row.last_auth_checked_at).toLocaleString():'Not checked']});
 const providerRows=(S.status.provider_health||[]).map(row=>[row.id,row.enabled?'Yes':'No',row.auto_approved?'Yes':'No',row.pool_strategy||'round-robin',row.catalog_implemented?(row.catalog_live_verified?'Live catalog':'Implemented catalog'):'Not implemented',row.verified_sources||0,badge(row.health||'untested',row.health==='healthy'?'good':row.health==='disabled'?'':'warn'),row.auth_status||'not_checked',(row.active||0)+' / '+(row.limit||'—'),row.accounts?.join(', ')||'None',row.sources?.join(', ')||'None',(row.completed||0)+' / '+(row.failures||0),row.last_validated_at?(new Date(row.last_validated_at).toLocaleString()+' · '+(row.last_validation_state||'recorded')):'Not checked',row.last_auth_checked_at?new Date(row.last_auth_checked_at).toLocaleString():'Not checked']);
 return [pageHead('Health','Recorded outcomes, cooldowns and account capacity.'),h('h2',{},'Provider health'),table(['Provider','Enabled','Auto','Pool strategy','Catalog','Verified sources','Health','Auth','In flight / limit','Accounts','Sources','Success / failure','Last validation','Last auth check'],providerRows,'No providers configured.'),h('h2',{},'Account health'),table(['Account','Provider','Enabled','Auto','Pool strategy','Weight','Credential state','Credential binding','Credential reference','Reviewed override','Health','Auth','In flight / limit','Sources','Success / failure','Last validated','Last auth check'],accountRows,'No accounts configured.'),h('h2',{},'Source health'),table(['Source','Account','State','Credential','In flight','Success / failure','Average duration','Cooldown until','Last validation'],S.config.sources.map(source=>{
  const validation=(S.status.sources||[]).find(x=>x.id===source.id)||{};
  const lastValidation=validation.last_validated_at?(new Date(validation.last_validated_at).toLocaleString()+' · '+(validation.last_validation_state||'recorded')):'Not checked';
  const s=S.status.sources.find(x=>x.id===source.id)||{};
  const provenance=(S.status.verification||[]).find(x=>x.source===source.id)||{};
  const credential=(provenance.credential_state||'not_configured')+(provenance.credential_version?' · v'+provenance.credential_version:'');
  return [source.id,source.account_id||'None',state(source),credential,s.active||0,(s.completed||0)+' / '+(s.failures||0),s.latency_ms?Math.round(s.latency_ms)+' ms':'Not measured',Date.parse(s.cooldown)>Date.now()?new Date(s.cooldown).toLocaleString():'—',lastValidation];
 })),h('h2',{},'Shared quota domains'),h('p',{class:'muted'},'Each domain is counted once, including requests finishing after a configuration change.'),table(['Domain','In flight / limit','Sources','Actions'],(S.status.quota_domains||[]).map(q=>[q.id+(q.retired?' (retiring)':''),q.active+' / '+q.limit,q.sources.join(', ')||'Finishing previous configuration',q.retired?null:button('Edit shared quota',()=>editQuota(q))]))];
}
function metrics(){
 const s=S.status;
 const sources=s.sources||[];
 const observed=key=>sources.reduce((sum,source)=>sum+Number(source[key]||0),0);
 const knownCost=sources.filter(source=>source.cost_known).reduce((sum,source)=>sum+Number(source.estimated_cost_usd||0),0);
 const costSources=sources.filter(source=>source.cost_known).length;
 return [pageHead('Metrics','Gateway counters across live configuration revisions.'),h('section',{class:'panel'},workloadSummary(s)),h('section',{class:'panel'},[
  ['Requests',s.requests],['Rejected at gateway',s.rejected],['Stream errors',s.stream_errors],['Response bytes',s.output_bytes],['Reserved request bytes',s.buffered_bytes],['Input tokens observed',observed('input_tokens')],['Output tokens observed',observed('output_tokens')],['Total tokens observed',observed('total_tokens')],['Estimated declared cost',costSources?'$'+knownCost.toFixed(6)+' ('+costSources+' source'+(costSources===1?'':'s')+')':'Unknown (no complete usage and rate pair)']
 ].map(([key,value])=>h('div',{class:'stat-line'},key,h('strong',{},typeof value==='number'?value.toLocaleString():String(value)))))];
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
function devices(){
 const target=h('section',{class:'panel'},h('p',{class:'muted'},'No device check has run in this tab. The check is read-only: it probes configured ADB metadata and never starts an emulator, opens an app, changes the clipboard or sends a message.'));
 const draw=result=>{
  const report=result.report||{},checks=Object.fromEntries((report.checks||[]).map(item=>[item.name,item]));
  const value=name=>checks[name]?.ok?'Pass':'Not ready';
  target.replaceChildren(
   h('div',{class:'device-summary'},
    h('div',{class:'stat-line'},'Doctor status',badge(report.ready?'Ready':'Needs attention',report.ready?'good':'warn')),
    h('div',{class:'stat-line'},'ADB',value('adb')),
    h('div',{class:'stat-line'},'Connected',value('device_online')),
    h('div',{class:'stat-line'},'Resolution',report.resolution||'Not observed'),
    h('div',{class:'stat-line'},'Foreground app',report.foreground_app||'Not observed'),
    result.restart_pending?h('p',{class:'warning'},'Device settings are saved but require a gateway restart before this check uses them.'):null
   ),
   h('h2',{},'App providers'),
   table(['Provider','Source','Installed','Login state','Last test'],(result.providers||[]).map(item=>[item.provider,item.source_id,item.installed?'Yes':'No',item.login_state==='detected'?'Detected':'Not observed',item.last_test==='pass'?'Pass':item.last_test==='failed'?'Failed':'Not tested']),'No app-device sources configured.'),
   h('h2',{},'Checks'),
   table(['Check','Status','Detail'],(report.checks||[]).map(item=>[item.name,item.ok?'Pass':'Not ready',item.detail]))
  );
 };
 const check=async()=>{target.replaceChildren(h('p',{class:'muted'},'Checking the configured physical device…'));draw(await api('/admin/device/check',{method:'POST'}));};
 return [pageHead('Devices','Connected Android device status and app-session evidence.',button('Check device',check,'primary')),...environment('devices').slice(1),target];
}
function browsers(){
 return [pageHead('Browsers','Dedicated profiles keep account browser state separate.',button('Owned processes',ownedBrowserProcesses),button('Discover profiles',async()=>{const rows=await api('/admin/browser_profiles/discover',{method:'POST'});dialog('Discovered browser profiles',[h('p',{class:'muted'},'Up to 256 profiles from standard browser metadata locations. Discovery does not read cookies, verify login or copy sessions. Use Add profile and Accounts → Login to create an isolated session.'),table(['Browser','Profile','Name','Location','Authentication'],rows.map(p=>[p.browser,p.profile,p.name,p.root,p.authentication]),'No readable profile metadata found.')])}),button('Check connections',async()=>{const rows=await api('/admin/browser_profiles/status',{method:'POST'});dialog('Browser connection status',table(['Profile','Connection','Browser','Pages','Bound accounts','Bound sources','Session limit','Authentication'],rows.map(p=>[p.profile,p.state,p.browser||'Unknown',p.pages,p.bound_accounts||0,p.bound_sources||0,p.max_sessions||'Not configured',p.authentication]),'No configured profiles.'))}),button('Add profile',()=>edit('browser_profiles',{id:'',enabled:true,engine:'chrome',cdp_url:'http://127.0.0.1:9223'},true),'primary')),table(['Profile','Engine','CDP endpoint','Accounts','Actions'],(S.config.browser_profiles||[]).map(p=>[
  p.id,p.engine,p.cdp_url,(S.config.accounts||[]).filter(a=>a.browser_profile_id===p.id).map(a=>a.display_name||a.id).join(', ')||'Unbound',[button(p.enabled?'Disable':'Enable',()=>toggle('browser_profiles',p)),button('Edit',()=>edit('browser_profiles',p)),button('Launch provider',()=>launchBrowserProfile(p)),button('Delete',()=>remove('browser_profiles',p),'danger')]
 ]),'No profiles. Add a profile, bind it from Accounts, then use Login.'),...environment('browsers').slice(1)];
}
function importToken(onSaved,allowedModes){
 const allowed=allowedModes?.()||null;
 const modeOptions=allowed?[
  ...((allowed.some(m=>['api_key','oauth','cookie'].includes(m)))?['environment']:[]),
  ...(allowed.includes('oauth')?['codex','gemini-cli','oauth']:[])
 ]:['environment','codex','gemini-cli','oauth'];
 const mode=select(modeOptions,modeOptions[0]||'environment');
 const source=select(S.config.sources.filter(s=>s.key_env).map(s=>s.id),'',true);
 const kindOptions=allowed?['api_key','oauth','cookie'].filter(k=>allowed.includes(k)):['api_key','oauth','cookie'];
 const kind=select(kindOptions,kindOptions[0]||'api_key');
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
  target.replaceChildren(h('p',{class:'muted'},'Metadata only. '+(result.truncated?'The list reached its 1,000-session limit. ':'')+'Sources without session management: '+(result.unsupported_sources.join(', ')||'None')+'. Failed reads: '+(result.failed_sources.join(', ')||'None')+'.'),table(['Source','Adapter','Inventory','Boundary'],(result.capabilities||[]).map(x=>[x.source,x.adapter,x.supported?'Supported':'Unavailable',x.reason])),table(['Session','Provider / account','Upstream conversation','Created','Last activity','State','Actions'],result.items.map(item=>[
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
 const draw=()=>eventTable.replaceChildren(table(['Sequence / time','Source / model','Protocol / purpose','Outcome','Release / upstream HTTP','Duration / first output','Declared tokens','Error category'],events.filter(e=>(source.value==='all'||e.source===source.value)&&(outcome.value==='all'||e.outcome===outcome.value)).map(e=>[e.sequence+' / '+new Date(e.finished_at).toLocaleString(),e.source+' / '+e.model,e.protocol+' / '+(e.validation?'Validation':'Dispatch'),e.outcome,e.status+' / '+(e.execution?.upstream_status||'Not observed'),e.duration_ms.toFixed(1)+' ms / '+(e.ttft_ms>0?e.ttft_ms.toFixed(1)+' ms':'Not observed'),e.execution?.usage_known?[Number(e.execution.input_tokens||0).toLocaleString()+' / '+Number(e.execution.output_tokens||0).toLocaleString()+' / '+Number(e.execution.total_tokens||0).toLocaleString()]:'Unknown',e.execution?.upstream_error||'None']),'No matching execution events.'));
 source.onchange=draw;outcome.onchange=draw;draw();
 return [pageHead('Activity','Runtime execution events, persisted checks and configuration history.',button('Refresh history',refresh)),h('h2',{},'Execution events'),h('p',{class:'muted'},'Latest 1,000 released execution attempts, newest first. Kept in memory across hot updates; cleared on process restart. Retries are separate attempts. Rejections before a source is acquired and administrative-only releases are not included. No prompts, response bodies, URLs or credentials are recorded.'),field('Event source',source),field('Event outcome',outcome),eventTable,h('h2',{},'Checks'),table(['Time','Kind','Source / model','Configuration revision','Status','Method'],[...(S.evidence||[])].reverse().map(v=>[new Date(v.checked_at).toLocaleString(),v.kind,v.resource+(v.model?' / '+v.model:''),v.revision+(v.revision===S.revision?' (current)':' (historical)'),v.status,v.method]),'No recorded checks.'),h('h2',{},'Configuration changes'),table(['Revision','Time','Change'],[...S.history].reverse().map(v=>[v.revision,new Date(v.created_at).toLocaleString(),v.summary]))];
}
function launchBrowserProfile(profile){
 const choices=S.catalog.filter(p=>p.base_url.startsWith('https://')).map(p=>p.id),account=(S.config.accounts||[]).find(a=>a.browser_profile_id===profile.id),provider=select(choices,account?.provider_id||'chatgpt-web');
 dialog('Launch provider browser',[field('Provider to open',provider),h('p',{},'Opens the selected provider origin in the isolated '+profile.id+' profile. Launching does not verify login. The browser remains open if this dialog closes.')],[button('Launch browser',async()=>{
  const result=await api('/admin/browser_profiles/setup-login',{method:'POST',body:JSON.stringify({profile,provider:provider.value,action:'launch'})});
  dialog('Browser launched',h('p',{},'Process '+result.pid+' started for '+profile.id+'. Complete login in its browser window.'),[button('View owned processes',ownedBrowserProcesses)]);
 },'primary')]);
}
async function ownedBrowserProcesses(){
 const rows=await api('/admin/browser_profiles/processes');
 dialog('Owned browser processes',[h('p',{class:'muted'},'Only launches retained by this gateway process are listed, including unsaved setup profiles. External browsers and launches from previous gateway runs cannot be stopped here. An exited launcher does not prove every browser child has exited.'),table(['Profile','PID','Started','State','Actions'],rows.map(p=>[p.profile,p.pid,new Date(p.started_at).toLocaleString(),p.state,['running','stop_requested'].includes(p.state)?button('Stop',()=>{
  dialog('Stop owned browser',h('p',{class:'warning'},'Stop process '+p.pid+' for '+p.profile+'? Active browser requests may fail and unsaved browser work may be lost. Profile data is kept. Only this recorded launch is targeted.'),[button('Cancel',ownedBrowserProcesses),button('Stop owned process',async()=>{await api('/admin/browser_profiles/processes/'+encodeURIComponent(p.id)+'/stop',{method:'POST',body:JSON.stringify({confirm:true})});await ownedBrowserProcesses()},'danger')]);
 }):null]),'No browser processes launched by this gateway.')],[button('Refresh processes',ownedBrowserProcesses)]);
}
function about(){
 return [pageHead('About','Clash of Tokens'),h('section',{class:'panel'},h('p',{},'A local gateway for model API sources and simulated providers.'),h('p',{},'Provider implementation, account credentials and live upstream verification are separate states. Catalog coverage does not establish live availability.'),h('p',{},'Changes are validated and written to a versioned configuration journal. Credentials are referenced, never included in that journal.'))];
}
function implementation(){
 const target=h('div',{class:'table-wrap'},h('p',{class:'muted'},'Loading implementation status…'));
 const load=async()=>{const result=await api('/admin/implementation');target.replaceChildren(table(['Provider','Kind','Adapter / factory','Catalog implementation','Configured accounts / sources','Catalog live evidence','Runtime checks','Reference / notes'],(result.items||[]).map(x=>[x.id,badge(x.kind),x.adapter+' / '+x.factory,x.implementation,x.configured_accounts+' / '+x.configured_sources,x.catalog_live_verified?'Recorded':'Not recorded',x.runtime_verified_models+' verified · '+x.runtime_failed_models+' failed',x.reference||x.notes||'—']),'No catalog entries.'));};
 setTimeout(()=>load().catch(e=>message(e.message,true)),0);
 return [pageHead('Implementation status','Catalog and runtime evidence are shown separately.',button('Refresh implementation',load)),h('p',{class:'muted'},'A factory means the shared adapter contract is available. Catalog live evidence is a published metadata flag. Runtime checks are point-in-time checks for configured sources and do not guarantee future availability.'),target];
}
function searchResults(query){
 const q=query.toLowerCase(),results=[];
 for(const p of S.catalog)if((p.id+' '+p.adapter).toLowerCase().includes(q))results.push([p.id,'Provider',()=>providerDetail(p)]);
 for(const kind of ['accounts','sources','groups'])for(const item of S.config[kind]||[])if([item.id,item.display_name,item.provider_id,item.provider,item.account_id,item.quota_domain,item.credential_ref,item.base_url,item.organization,item.project].filter(Boolean).join(' ').toLowerCase().includes(q))results.push([item.display_name||item.id,title(kind),()=>kind==='sources'?sourceDetail(item):edit(kind,item)]);
 for(const source of S.config.sources)for(const model of source.models)if([model.id,model.upstream,model.canonical_model,model.declared_model,source.id,source.provider].filter(Boolean).join(' ').toLowerCase().includes(q))results.push([model.id+' · '+source.id,'Model',()=>bulkModels([{source,model}])]);
 for(const profile of S.config.browser_profiles||[])if((profile.id+' '+profile.engine).toLowerCase().includes(q))results.push([profile.id,'Browser profile',()=>edit('browser_profiles',profile)]);
 if(('device android '+(S.config.device?.serial||'')).toLowerCase().includes(q))results.push(['Android device settings','Device',()=>{$('search').value='';location.hash='devices';render()}]);
 for(const c of S.credentials)if([c.id,c.kind,c.source,c.domain].filter(Boolean).join(' ').toLowerCase().includes(q))results.push([c.id,'Credential',()=>credentialForm(c)]);
 return [pageHead('Search',results.length+' matching entries'),h('div',{class:'search-results'},results.map(([name,type,action])=>h('div',{class:'search-result'},button(name,action),h('span',{},type))))];
}
function render(){
 const route=location.hash.slice(1).split('/')[0]||'overview';
 for(const a of $('navigation').querySelectorAll('a'))a.setAttribute('aria-current',a.hash==='#'+route?'page':'false');
 if(!S.config){$('view').replaceChildren(connectView());return}
 if($('search').value){$('view').replaceChildren(...searchResults($('search').value));return}
 const renderers={overview,providers,accounts,credentials,sources,models,groups,routing,health,metrics,config:configuration,logs:activity,implementation,about,browsers,devices,sessions};
 $('view').replaceChildren(...(renderers[route]||overview)().filter(x=>x!=null));
}
for(const [group,items] of pages){$('navigation').append(h('div',{class:'nav-group'},group),...items.map(id=>h('a',{href:'#'+id},labels[id])))}
$('access').onclick=()=>{if(!S.token){$('view').replaceChildren(connectView());return}dialog('Gateway connection',h('p',{},'Disconnect to clear the management key and loaded configuration from this tab.'),[button('Disconnect',()=>{Object.assign(S,{token:'',config:null,credentials:[],history:[],catalog:[],status:{sources:[]}});$('access').textContent='Connect';$('dialog').close();message('Disconnected.');render()})])};
$('refresh').onclick=()=>buttonAction(refresh);
$('search').oninput=render;
window.addEventListener('hashchange',()=>{$('search').value='';render()});
render();
