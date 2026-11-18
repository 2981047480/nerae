import AppKit
import WebKit
final class Probe: NSObject, WKNavigationDelegate {
    let webView = WKWebView(frame: NSRect(x: 0, y: 0, width: 390, height: 844))
    let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 390, height: 844), styleMask: .borderless, backing: .buffered, defer: false)
    override init() { super.init(); window.contentView = webView; webView.navigationDelegate = self }
    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) {
            webView.evaluateJavaScript("document.querySelector('.swipe-card__copy')?.click(); true") { _, error in
                if let error = error { print("copy error",error); NSApp.terminate(nil); return }
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                    webView.evaluateJavaScript("JSON.stringify([...document.querySelectorAll('.swipe-card__face')].map(e=>e.textContent))") { result, error in
                        print("copied:",result ?? String(describing:error))
                    }
                    let config = WKSnapshotConfiguration(); config.rect = webView.bounds
                    webView.takeSnapshot(with: config) { image, error in
                        if let data = image?.tiffRepresentation, let bitmap = NSBitmapImageRep(data: data),
                           let png = bitmap.representation(using: .png, properties: [:]) {
                            try? png.write(to: URL(fileURLWithPath:"/private/tmp/nerae-draft-v2-visual/copy.png"))
                        } else { print("snapshot error",String(describing:error)) }
                        NSApp.terminate(nil)
                    }
                }
            }
        }
    }
}
let app = NSApplication.shared; app.setActivationPolicy(.prohibited)
let probe = Probe(); probe.webView.load(URLRequest(url: URL(string:"http://127.0.0.1:62592/")!)); app.run()
