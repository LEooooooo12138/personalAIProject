const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const script = fs.readFileSync(path.join(__dirname, '../../go-agent/static/chat-widget.js'), 'utf8');
const tick = () => new Promise(resolve => setImmediate(resolve));
function element() {
  const children = [], selectors = new Map(), listeners = new Map();
  return {children, style:{}, value:'', classList:{add(){},remove(){},contains(){return false;}},
    appendChild(child){ children.push(child); }, prepend(){}, focus(){}, remove(){}, removeAttribute(){},
    addEventListener(name, callback){listeners.set(name, callback);},
    fire(name, event={}){listeners.get(name)?.(event);},
    querySelector(selector){if(!selectors.has(selector)) selectors.set(selector, element()); return selectors.get(selector);},
    set innerHTML(value){this.html=value; children.length=0;}, get innerHTML(){return this.html||'';}
  };
}
async function widget(history = {status:404, messages:[]}) {
  const storage = new Map([['chat-widget-sid','old-owner-session']]);
  const document = {head:element(), body:element(), createElement:element, getElementById(){return null;}};
  const sent = [], sockets = [], requests = [];
  class Socket {
    static OPEN = 1;
    constructor(){this.readyState=1;sockets.push(this);}
    send(text){sent.push(JSON.parse(text));}
    close(){this.readyState=3;}
    receive(data){this.onmessage({data:JSON.stringify(data)});}
  }
  const context = {window:{}, document, location:{protocol:'http:',host:'localhost'},
    localStorage:{getItem:key=>storage.get(key),setItem:(key,value)=>storage.set(key,value),removeItem:key=>storage.delete(key)},
    WebSocket:Socket, setTimeout,clearTimeout,setInterval,clearInterval,
    fetch:async(url,opts)=>{requests.push({url,opts}); if(url.endsWith('/auth/browser'))return {ok:true,status:200};
      const response = await (typeof history === 'function' ? history() : history);
      return {ok:response.status===200,status:response.status,json:async()=>response.status===200 ? {messages:response.messages||[]} : {error:'unavailable'}};
    }};
  vm.runInNewContext(script, context);
  context.window.ChatWidget.init({greeting:''});
  const container = document.body.children[0];
  container.querySelector('.cw-toggle').fire('click');
  await tick();
  sockets[0].onopen();
  await tick();
  return {storage,sent,requests,socket:sockets[0],send(text){container.querySelector('.cw-input').value=text;container.querySelector('.cw-send').fire('click');}};
}
test('expired identity history 404 clears SID; the next message starts a fresh conversation', async()=>{
  const w=await widget();w.send('new question');
  assert.equal(w.sent[0].session_id,'');
  assert.equal(w.storage.has('chat-widget-sid'),false);
  w.socket.receive({type:'session',session_id:'new-session'});
  assert.equal(w.storage.get('chat-widget-sid'),'new-session');
  w.send('follow up');assert.equal(w.sent[1].session_id,'new-session');
});
test('structured missing-session error resets SID without replaying input', async()=>{
  const w=await widget({status:200});w.send('first');
  w.socket.receive({type:'error',code:'session_not_found',session_id:'old-owner-session',message:'session not found'});
  assert.equal(w.sent.length,1);
  w.send('retry explicitly');assert.equal(w.sent[1].session_id,'');
});
test('transient history and websocket errors preserve the conversation', async()=>{
  const w=await widget({status:500});
  for(const code of ['session_unavailable','generation_failed',''])w.socket.receive({type:'error',code,message:'temporary failure'});
  w.send('retry');assert.equal(w.sent[0].session_id,'old-owner-session');
});
test('late history 404 cannot erase a newly established session', async()=>{
  let respond;const waiting = new Promise(resolve=>{respond=resolve;});
  const w=await widget(()=>waiting);
  w.socket.receive({type:'session',session_id:'new-session'});
  respond({status:404});await tick();w.send('continue');
  assert.equal(w.sent[0].session_id,'new-session');assert.equal(w.storage.get('chat-widget-sid'),'new-session');
});
test('late websocket missing-session error cannot erase a new session', async()=>{
  const w=await widget({status:200});
  w.socket.receive({type:'session',session_id:'new-session'});
  w.socket.receive({type:'error',code:'session_not_found',session_id:'old-owner-session',message:'old failure'});
  w.send('continue');assert.equal(w.sent[0].session_id,'new-session');
});
