import Foundation
import JavaScriptCore

func fail(_ message: String) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(1)
}

guard CommandLine.arguments.count == 2,
      let context = JSContext() else { fail("Cannot create JavaScriptCore probe") }
var raised = false
context.exceptionHandler = { _, error in
    raised = true
    if let error { FileHandle.standardError.write(Data(("JavaScriptCore exception: \(error)\n").utf8)) }
}
let initial = context.evaluateScript("typeof URL")?.toString() ?? ""
guard initial == "undefined" else { fail("Probe expects a JavaScriptCore context without URL") }
guard let source = try? String(contentsOfFile: CommandLine.arguments[1], encoding: .utf8) else {
    fail("Cannot read bundled probe")
}
context.evaluateScript(source)
guard !raised, let value = context.evaluateScript("__neraeUrlProbe()"),
      !raised, value.toBool() else { fail("URL polyfill or adapter construction probe failed") }
print("PASS: core-js URL and real auth/recording constructors ran in a bare macOS JavaScriptCore context; no device or DCloud runtime was tested.")
