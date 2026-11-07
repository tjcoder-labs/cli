// No debugger attachment is made by the stealth dispatcher. Tab IDs double as
// virtual CDP session IDs so existing Go browser actions keep their public API.
export function makeDispatcher(api) {
  async function script(tabId, func, args = []) {
    const r = await api.scripting.executeScript({target: {tabId}, world: 'ISOLATED', func, args});
    if (r[0]?.error) throw new Error(r[0].error.message || String(r[0].error));
    return r[0]?.result;
  }
  return async function dispatch(request) {
    const {method, params: p = {}, sessionId, transport = 'stealth'} = request;
    if (!['stealth', 'cdp'].includes(transport)) throw new Error('Unknown transport');
    if (method === 'Bridge.ping') return {connected: true, version: api.runtime.getManifest().version, transport};
    if (transport === 'cdp') throw new Error('unsupported: native CDP tier is deferred to P1; explicit port + transport=cdp uses legacy raw CDP');
    const tabKey = sessionId || p.targetId;
    const tabId = Number(String(tabKey).replace(/^incognito:/, ""));
    const tabKeyFor = t => (t.incognito ? "incognito:" : "") + t.id;
    const requireTab = () => { if (!Number.isInteger(tabId) || tabId <= 0) throw new Error('Valid tab_id required'); };
    switch (method) {
      case 'Target.getTargets': return {targetInfos: (await api.tabs.query({})).map(t => ({targetId: tabKeyFor(t), type: 'page', title: t.title || '', url: t.url || '', incognito: t.incognito}))};
      case 'Target.createTarget': {
        let t;
        if (p.incognito) {
          if (!await api.extension.isAllowedIncognitoAccess()) throw new Error('Enable Allow in incognito in the extension details first');
          const w = await api.windows.create({incognito: true, url: p.url || 'about:blank'}); t = w.tabs?.[0];
          if (!t) throw new Error('Private window did not return a tab');
        } else t = await api.tabs.create({url: p.url || 'about:blank'});
        return {targetId: p.incognito ? "incognito:" + t.id : tabKeyFor(t)};
      }
      case 'Target.closeTarget': requireTab(); await api.tabs.remove(tabId); return {success: true};
      case 'Target.attachToTarget': requireTab(); await api.tabs.get(tabId); return {sessionId: String(tabKey)};
      case 'Target.detachFromTarget': return {};
      case 'Page.enable': case 'Runtime.enable': case 'DOM.enable': return {};
      case 'Page.navigate': requireTab(); await api.tabs.update(tabId, {url: p.url}); return {};
      case 'Page.reload': requireTab(); await api.tabs.reload(tabId); return {};
      case 'Bridge.back': requireTab(); await api.tabs.goBack(tabId); return {};
      case 'Bridge.forward': requireTab(); await api.tabs.goForward(tabId); return {};
      case 'Bridge.getDOM': {
        requireTab();
        const value = await script(tabId, (selector, max) => {
          const node = selector ? document.querySelector(selector) : document.documentElement;
          return (node?.outerHTML || '').slice(0, max || 200000);
        }, [p.selector || '', p.maxChars || 200000]);
        return {html: value};
      }
      case 'Runtime.evaluate': {
        requireTab();
        // MV3 forbids eval in ordinary content scripts. User Scripts is the
        // supported isolated-world API for caller-supplied JavaScript. It needs
        // the per-extension Allow User Scripts toggle (Chrome 135+).
        if (!api.userScripts?.execute) throw new Error('Evaluate requires Chrome 135+ and Allow User Scripts in extension details');
        await api.userScripts.configureWorld({csp: "script-src 'self' 'unsafe-eval'", messaging: false});
        const code = `(async()=>{try {const value = ${p.awaitPromise === false ? '' : 'await '}(0,eval)(${JSON.stringify(p.expression)});return {result:{type:typeof value,value:value===undefined?null:value}};} catch(e){return {exceptionDetails:{text:String(e)}};}})()`;
        const r = await api.userScripts.execute({target: {tabId}, world: 'USER_SCRIPT', js: [{code}]});
        if (r[0]?.error) throw new Error(String(r[0].error));
        return r[0]?.result || {result: {type: 'undefined', value: null}};
      }
      case 'Page.captureScreenshot': {
        requireTab();
        const t = await api.tabs.get(tabId);
        // captureVisibleTab only captures the active tab. Explicitly focus it,
        // never silently return a screenshot of a different page.
        await api.windows.update(t.windowId, {focused: true});
        await api.tabs.update(tabId, {active: true});
        const data = await api.tabs.captureVisibleTab(t.windowId, {format: 'jpeg', quality: p.quality || 80});
        return {data: data.split(',')[1]};
      }
      default: throw new Error(`unsupported: ${method} in stealth transport (P1/P2 action)`);
    }
  };
}
