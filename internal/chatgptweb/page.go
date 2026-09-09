package chatgptweb

// These scripts operate only in a gateway-owned tab. Authentication remains in
// the browser: neither cookies nor access tokens cross the CDP result boundary.
const pageHelpers = `
async function boundedJSON(response, limit = 8388608) {
 if (!response.ok) throw new Error('http_' + response.status);
 const reader = response.body.getReader(); let size = 0, chunks = [];
 try { for (;;) { const r = await reader.read(); if (r.done) break; size += r.value.byteLength;
  if (size > limit) throw new Error('conversation_too_large'); chunks.push(r.value); }
 } finally { await reader.cancel(); }
 const all = new Uint8Array(size); let at = 0; for (const chunk of chunks) { all.set(chunk, at); at += chunk.length; }
 return JSON.parse(new TextDecoder().decode(all));
}
async function auth() {
 const a = await boundedJSON(await fetch('/api/auth/session', {credentials:'include'}), 65536);
 if (!a.accessToken || !a.user || !a.user.id) throw new Error('login_required'); return a;
}
function composer() {
 const selectors = ['#prompt-textarea[contenteditable="true"]', '.ProseMirror[contenteditable="true"][role="textbox"]', 'textarea#prompt-textarea'];
 for (const selector of selectors) { const el = [...document.querySelectorAll(selector)].find(e => e.getClientRects().length && !e.disabled); if (el) return el; }
 return null;
}
function composerText(el) {
 if (el.tagName === 'TEXTAREA') return el.value;
 return el.innerText.replace(/\r\n/g,'\n');
}
function conversationID() { const match = location.pathname.match(/^\/c\/([a-zA-Z0-9-]+)$/); return match ? match[1] : ''; }
`

const inspectPage = `
 const a = await auth(); const el = composer();
 return {account:a.user.id, ready:!!el, draft:el?composerText(el):'', conversation:conversationID()};
`

// The path is followed from current_node back to the saved last_node. A
// matching prompt anywhere else in the conversation cannot become this turn.
const readTurn = `
 const a = await auth(); if (a.user.id !== args.account) throw new Error('account_changed');
 const conv = conversationID(); if (!conv) return {pending:true};
 if (args.conversation && conv !== args.conversation) throw new Error('conversation_changed');
 const data = await boundedJSON(await fetch('/backend-api/conversation/' + encodeURIComponent(conv), {
  credentials:'include', headers:{Authorization:'Bearer '+a.accessToken}
 }));
 if (!data.mapping || !data.current_node) throw new Error('conversation_schema_changed');
 let cursor = data.current_node; const branch = []; const visited = new Set();
 while (cursor && cursor !== args.parent) {
  if (visited.has(cursor) || visited.size > 4096) throw new Error('invalid_conversation_graph');
  visited.add(cursor); const node = data.mapping[cursor]; if (!node) throw new Error('missing_conversation_node');
  branch.push(node); cursor = node.parent;
 }
 if (args.parent && cursor !== args.parent) throw new Error('conversation_branch_changed');
 branch.reverse();
 if (args.before) return {conversation:conv,node:data.current_node};
 const users = branch.filter(n => n.message && n.message.author?.role === 'user' && n.message.content?.content_type === 'text');
 if (!users.length) return {conversation:conv,pending:true};
 if (users.length !== 1 || !Array.isArray(users[0].message.content.parts) || users[0].message.content.parts.some(p=>typeof p!=='string') || users[0].message.content.parts.join('') !== args.prompt) throw new Error('submitted_turn_does_not_match');
 const user = users[0]; let answer = null; const index = branch.indexOf(user);
 for (const node of branch.slice(index+1)) {
  const m = node.message;
  if (m && m.author?.role === 'assistant' && m.content?.content_type === 'text' && m.metadata?.channel !== 'analysis' && (!m.recipient || m.recipient === 'all')) answer = node;
 }
 if (!answer) return {conversation:conv,pending:true};
 const m = answer.message; if (!Array.isArray(m.content.parts) || m.content.parts.some(p=>typeof p!=='string')) throw new Error('non_text_response');
 const text = m.content.parts.join(''); if (new TextEncoder().encode(text).length > args.max_response) throw new Error('response_too_large');
 return {conversation:conv,node:answer.id || m.id,user_node:user.id || user.message.id,text,done:m.end_turn===true && m.status==='finished_successfully',model:m.metadata?.model_slug || '',pending:false};
`
