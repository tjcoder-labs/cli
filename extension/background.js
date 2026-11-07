import {makeDispatcher} from './dispatch.js';
const dispatch = makeDispatcher(chrome);
let port;
function connect() {
  if (port) return;
  try {
    const native = chrome.runtime.connectNative('com.tjcoder.browser'); port = native;
    native.postMessage({method: "Bridge.hello", params: {incognito: chrome.extension.inIncognitoContext}});
    native.onMessage.addListener(async request => {
      try { native.postMessage({id: request.id, result: await dispatch(request)}); }
      catch (e) { try { native.postMessage({id: request.id, error: {code: -32000, message: String(e.message || e)}}); } catch {} }
    });
    native.onDisconnect.addListener(() => {void chrome.runtime.lastError; if (port === native) port = undefined;});
  } catch (e) { console.warn('Coder native host unavailable:', e.message); }
}
chrome.runtime.onInstalled.addListener(connect);
chrome.runtime.onStartup.addListener(connect);
chrome.action.onClicked.addListener(connect);
chrome.alarms.create('coder-reconnect', {periodInMinutes: 0.5});
chrome.alarms.onAlarm.addListener(a => {if (a.name === 'coder-reconnect') connect();});
connect();
