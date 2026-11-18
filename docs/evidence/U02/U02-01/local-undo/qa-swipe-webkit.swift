import AppKit
import WebKit

final class Probe: NSObject, WKNavigationDelegate {
    let webView = WKWebView(frame: NSRect(x: 0, y: 0, width: 390, height: 844))
    let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 390, height: 844), styleMask: .borderless, backing: .buffered, defer: false)
    override init() { super.init(); window.contentView = webView; webView.navigationDelegate = self }
    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) {
            let swipe = """
            (() => {
              const card = document.querySelector('.swipe-card');
              if (!card) return 'missing card';
              const logs=[]; window.__logs=logs; window.addEventListener('error',e=>logs.push(['error',e.message])); for (const n of ['touchstart','touchmove','touchend']) card.addEventListener(n,e=>logs.push([n,e.touches?.length,e.touches?.[0]?.clientX]));
              const items=[['touchstart',220,200],['touchmove',120,202],['touchend',120,202]];
              items.forEach(([name,x,y],idx) => setTimeout(() => {
                const e = new Event(name,{bubbles:true,cancelable:true});
                const p = {identifier:1,clientX:x,clientY:y,pageX:x,pageY:y,force:1};
                Object.defineProperty(e,'touches',{value:name === 'touchend' ? [] : [p]});
                Object.defineProperty(e,'changedTouches',{value:[p]});
                Object.defineProperty(e,'targetTouches',{value:name === 'touchend' ? [] : [p]});
                card.dispatchEvent(e);
                setTimeout(()=>logs.push(['style', name, card.querySelector('.swipe-card__face')?.getAttribute('style')]),10);
              },idx*80));
              return JSON.stringify({ logs, html:card.outerHTML.slice(0,1500), keys:Object.keys(card).filter(x=>x.startsWith('_')) });
            })()
            """
            webView.evaluateJavaScript(swipe) { result, error in
                print("swipe:", result ?? String(describing:error))
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                    webView.evaluateJavaScript("JSON.stringify({logs:window.__logs,html:document.querySelector('.swipe-card')?.outerHTML.slice(0,700)})") { result, _ in print("after:",result ?? "none") }
                    self.snapshot("/private/tmp/nerae-draft-v2-visual/swipe-rail.png") {
                        webView.evaluateJavaScript("document.querySelector('.swipe-card__remove')?.click(); true") { _, _ in
                            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                                self.snapshot("/private/tmp/nerae-draft-v2-visual/removed.png") {
                                    let check = """
                                    (() => {
                                      const results = {};
                                      results.undoShown = !!document.querySelector('.undo-notice');
                                      results.removedShown = document.querySelector('.removed-groups__toggle')?.textContent.includes('已移除') === true;
                                      document.querySelector('.undo-notice button, .undo-notice uni-button')?.click();
                                      return results;
                                    })()
                                    """
                                    webView.evaluateJavaScript(check) { result, error in
                                        print("removal:", result ?? String(describing:error))
                                        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                                            webView.evaluateJavaScript("({ restored: !!document.querySelector('.swipe-card'), text: document.querySelector('.swipe-card')?.textContent || '' })") { result, error in
                                                print("restore:", result ?? String(describing:error)); NSApp.terminate(nil)
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    func snapshot(_ path: String, completion: @escaping () -> Void) {
        let config = WKSnapshotConfiguration(); config.rect = webView.bounds
        webView.takeSnapshot(with: config) { image, error in
            if let data = image?.tiffRepresentation, let bitmap = NSBitmapImageRep(data: data),
               let png = bitmap.representation(using: .png, properties: [:]) { try? png.write(to: URL(fileURLWithPath:path)) }
            else { print("snapshot error:", String(describing:error)) }
            completion()
        }
    }
}
let app = NSApplication.shared; app.setActivationPolicy(.prohibited)
let probe = Probe(); probe.webView.load(URLRequest(url: URL(string:"http://127.0.0.1:62592/")!)); app.run()
