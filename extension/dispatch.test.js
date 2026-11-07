import {test} from 'node:test';
import assert from 'node:assert/strict';
import {makeDispatcher} from './dispatch.js';
function setup() {
 const calls = [];
 const method = (name, result) => async (...args) => {calls.push([name, ...args]);return result};
 const api = {
  runtime: {getManifest: () => ({version:'0.9.254'})},
  extension: {isAllowedIncognitoAccess: async () => true},
  tabs: {query: method('query', [{id:1,title:'Test',url:'https://example.com',incognito:false}]), create:method('create',{id:2}),get:method('get',{id:1,windowId:4}),update:method('update',{}),reload:method('reload'),remove:method('remove'),goBack:method('back'),goForward:method('forward'),captureVisibleTab:method('capture','data:image/jpeg;base64,abcd')},
  windows: {create:method('private',{tabs:[{id:3,incognito:true}]}), update:method('focus',{})},
  scripting: {executeScript:method('script',[{result:'<html>Test</html>'}])},
  debugger: {attach:()=>{throw new Error('debugger must never attach')}},
  userScripts: {configureWorld:method('world'),execute:method('evaluate',[{result:{result:{type:'number',value:2}}}])}
 }; return {dispatch:makeDispatcher(api),calls,api};
}
test('P0 mappings never attach debugger',async()=>{
 const {dispatch:d,calls}=setup();
 assert.equal((await d({method:'Bridge.ping'})).connected,true);
 assert.equal((await d({method:'Target.getTargets'})).targetInfos[0].targetId,'1');
 assert.equal((await d({method:'Target.createTarget',params:{url:'https://example.com'}})).targetId,'2');
 assert.equal((await d({method:'Target.createTarget',params:{incognito:true}})).targetId,'incognito:3');
 assert.equal((await d({method:'Target.attachToTarget',params:{targetId:'incognito:3'}})).sessionId,'incognito:3');
 await d({method:'Page.navigate',sessionId:'1',params:{url:'https://example.org'}});
 await d({method:'Page.reload',sessionId:'1'});
 await d({method:'Bridge.back',sessionId:'1'});await d({method:'Bridge.forward',sessionId:'1'});
 assert.equal((await d({method:'Bridge.getDOM',sessionId:'1'})).html,'<html>Test</html>');
 assert.equal((await d({method:'Runtime.evaluate',sessionId:'1',params:{expression:'1+1'}})).result.value,2);
 assert.equal((await d({method:'Page.captureScreenshot',sessionId:'1'})).data,'abcd');
 await d({method:'Target.closeTarget',params:{targetId:'1'}});
 assert(calls.some(c=>c[0]==='focus'));assert(calls.some(c=>c[0]==='script' && c[1].world==='ISOLATED'));
});
test('P1/P2 errors are explicit, never fall back to debugger',async()=>{
 const {dispatch:d}=setup();await assert.rejects(d({method:'Input.dispatchMouseEvent',sessionId:'1'}),/unsupported/);await assert.rejects(d({method:'Page.navigate',transport:'cdp'}),/unsupported/);await assert.rejects(d({method:'Page.navigate',transport:'invalid'}),/Unknown/);
});
test('evaluate requires userScripts opt-in and incognito requires access',async()=>{
 const {dispatch:d,api}=setup();delete api.userScripts;await assert.rejects(d({method:'Runtime.evaluate',sessionId:'1'}),/Allow User Scripts/);api.extension.isAllowedIncognitoAccess=async()=>false;await assert.rejects(d({method:'Target.createTarget',params:{incognito:true}}),/Allow in incognito/);
});
