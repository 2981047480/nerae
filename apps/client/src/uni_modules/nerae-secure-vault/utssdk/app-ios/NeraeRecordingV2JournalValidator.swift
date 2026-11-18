import Foundation
import CoreFoundation

// Validates only the new request shape after strict JSON parsing. It does not
// authorize dispatch; the native journal still checks its session and binding.
enum NeraeRecordingV2JournalValidator {
    static func validate(path: String, body: [String: Any], event: String) -> Bool {
        func keys(_ object: [String: Any], _ allowed: Set<String>, _ required: Set<String>) -> Bool {
            let present = Set(object.keys)
            return present.isSubset(of: allowed) && required.isSubset(of: present)
        }
        func optional(_ object: [String: Any], _ key: String, _ check: (Any) -> Bool) -> Bool {
            guard let value = object[key] else { return true }; return check(value)
        }
        func integer(_ value: Any?) -> Int64? {
            guard let number = value as? NSNumber, CFGetTypeID(number) != CFBooleanGetTypeID(), number.doubleValue.isFinite,
                  number.doubleValue.rounded() == number.doubleValue, number.doubleValue >= 0,
                  number.doubleValue <= 9_007_199_254_740_991 else { return nil }
            return number.int64Value
        }
        func bool(_ value: Any?) -> Bool {
            guard let number = value as? NSNumber else { return false }
            return CFGetTypeID(number) == CFBooleanGetTypeID()
        }
        func validKey(_ value: Any?) -> String? {
            guard let text = value as? String, (8...64).contains(text.unicodeScalars.count),
                  !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
                  !text.contains("\r"), !text.contains("\n") else { return nil }
            return text
        }
        func uniqueKeys(_ value: Any?, limit: Int) -> [String]? {
            guard let array = value as? [Any], array.count <= limit else { return nil }
            var result: [String] = []; var seen = Set<Data>()
            for item in array {
                guard let text = validKey(item), seen.insert(Data(text.utf8)).inserted else { return nil }
                result.append(text)
            }
            return result
        }
        func recordingDate(_ value: Any) -> Bool {
            guard let text = value as? String, text.range(of:#"\A[1-9][0-9]{3}-[0-9]{2}-[0-9]{2}\z"#,options:.regularExpression) != nil else { return false }
            let parts = text.split(separator:"-").compactMap { Int($0) }
            guard parts.count == 3, (1...12).contains(parts[1]) else { return false }
            let year = parts[0], month = parts[1], day = parts[2]
            let leap = year % 4 == 0 && (year % 100 != 0 || year % 400 == 0)
            let days = month == 2 ? (leap ? 29 : 28) : ([4,6,9,11].contains(month) ? 30 : 31)
            return (1...days).contains(day)
        }
        func weight(_ value: Any) -> Bool {
            guard let text = value as? String else { return false }
            return text.range(of: #"\A[0-9]{1,6}(?:\.[0-9]{1,2})?\z"#, options: .regularExpression) != nil
        }
        func reps(_ value: Any) -> Bool { guard let n = integer(value) else { return false }; return (1...100).contains(n) }
        func side(_ value: Any) -> Bool {
            guard let item = value as? [String: Any], keys(item,["state","weightKg","reps"],["state"]),
                  let state = item["state"] as? String, ["unknown","performed","not_performed"].contains(state) else { return false }
            return optional(item,"weightKg",weight) && optional(item,"reps",reps)
        }
        func measurement(_ value: Any) -> Bool {
            guard let item = value as? [String: Any], keys(item,["method","weightKg","reps","bodyweightKg","adjustmentKg","left","right"],["method"]),
                  let method = item["method"] as? String, optional(item,"weightKg",weight), optional(item,"bodyweightKg",weight),
                  optional(item,"adjustmentKg",weight), optional(item,"reps",reps), optional(item,"left",side), optional(item,"right",side) else { return false }
            if let bodyweight = item["bodyweightKg"] as? String, (Double(bodyweight) ?? 0) <= 0 { return false }
            let present = Set(item.keys)
            switch method {
            case "total", "pair_each", "direct": return present.isDisjoint(with:["bodyweightKg","adjustmentKg","left","right"])
            case "sides": return present.isDisjoint(with:["weightKg","reps","bodyweightKg","adjustmentKg"])
            case "bodyweight": return present.isDisjoint(with:["weightKg","adjustmentKg","left","right"])
            case "bodyweight_extra", "bodyweight_assisted":
                if !present.isDisjoint(with:["weightKg","left","right"]) { return false }
                if method == "bodyweight_assisted", let bodyweight = item["bodyweightKg"] as? String, let adjustment = item["adjustmentKg"] as? String,
                   (Double(adjustment) ?? 0) > (Double(bodyweight) ?? 0) { return false }
                return true
            default: return false
            }
        }
        if path == "/me/recordings" {
            guard keys(body,["source","date","timeZone","clientEventId","mode"],["source","date","clientEventId","mode"]),
                  body["source"] as? String == "free", let date = body["date"] as? String,
                  date.range(of:#"\A[0-9]{4}-[0-9]{2}-[0-9]{2}\z"#,options:.regularExpression) != nil,
                  let submitted = body["clientEventId"] as? String, submitted.utf8.elementsEqual(event.utf8),
                  let mode = body["mode"] as? String, ["after_workout","per_set"].contains(mode),
                  optional(body,"timeZone",{ guard let value = $0 as? String else { return false }; return (1...64).contains(value.count) }) else { return false }
            return true
        }
        guard (integer(body["expectedVersion"]) ?? 0) >= 1 else { return false }
        if path.hasSuffix("/finish") { return keys(body,["expectedVersion"],["expectedVersion"]) }
        guard path.hasSuffix("/sets"), keys(body,["expectedVersion","date","mode","addEntries","sets","removeRecordKeys","removeRecords","restoreRecordKeys","removeEntryKeys","restoreEntryKeys","exerciseEntryKeys"],["expectedVersion","exerciseEntryKeys"]),
              let order = uniqueKeys(body["exerciseEntryKeys"],limit:2000),
              optional(body,"date",recordingDate), optional(body,"mode",{ guard let m = $0 as? String else { return false }; return ["after_workout","per_set"].contains(m) }) else { return false }
        let orderSet = Set(order.map { Data($0.utf8) })
        var added = Set<Data>()
        guard let entries = (body["addEntries"] ?? []) as? [Any], entries.count <= 2000 else { return false }
        for raw in entries {
            guard let item = raw as? [String:Any], keys(item,["exerciseEntryKey","exerciseName"],["exerciseEntryKey","exerciseName"]),
                  let key = validKey(item["exerciseEntryKey"]), let name = item["exerciseName"] as? String,
                  (1...60).contains(name.unicodeScalars.count), !name.trimmingCharacters(in:.whitespacesAndNewlines).isEmpty,
                  orderSet.contains(Data(key.utf8)), added.insert(Data(key.utf8)).inserted else { return false }
        }
        guard let sets = (body["sets"] ?? []) as? [Any], sets.count <= 200 else { return false }
        var records = Set<Data>(); var indexes = Set<Data>()
        for raw in sets {
            guard let item = raw as? [String:Any], keys(item,["recordKey","exerciseEntryKey","setIndex","confirmed","rpe","measurement"],["recordKey","exerciseEntryKey","setIndex","confirmed","measurement"]),
                  let record = validKey(item["recordKey"]), let entry = validKey(item["exerciseEntryKey"]), let index = integer(item["setIndex"]), index >= 1,
                  bool(item["confirmed"]), let m = item["measurement"], measurement(m), orderSet.contains(Data(entry.utf8)),
                  optional(item,"rpe",{ guard let text = $0 as? String else { return false }; return text.range(of:#"\A(?:[1-9](?:\.[0-9])?|10(?:\.0)?)\z"#,options:.regularExpression) != nil }),
                  records.insert(Data(record.utf8)).inserted else { return false }
            var indexKey = Data(entry.utf8); indexKey.append(0); indexKey.append(contentsOf:String(index).utf8)
            if !indexes.insert(indexKey).inserted { return false }
        }
        guard let removeKeys=uniqueKeys(body["removeRecordKeys"] ?? [],limit:200), let restoreKeys=uniqueKeys(body["restoreRecordKeys"] ?? [],limit:200),
              let removeEntries=uniqueKeys(body["removeEntryKeys"] ?? [],limit:200), let restoreEntries=uniqueKeys(body["restoreEntryKeys"] ?? [],limit:200),
              let removalRecords=(body["removeRecords"] ?? []) as? [Any], removalRecords.count <= 200 else { return false }
        var removed=Set(removeKeys.map { Data($0.utf8) })
        for raw in removalRecords {
            guard let item = raw as? [String:Any], keys(item,["recordKey","removedPosition"],["recordKey","removedPosition"]),
                  let key=validKey(item["recordKey"]), let position=integer(item["removedPosition"]), position >= 1,
                  removed.insert(Data(key.utf8)).inserted else { return false }
        }
        let restored=Set(restoreKeys.map { Data($0.utf8) })
        let entryRemoved=Set(removeEntries.map { Data($0.utf8) }); let entryRestored=Set(restoreEntries.map { Data($0.utf8) })
        if !entryRemoved.isDisjoint(with:entryRestored) || !restored.intersection(removed).isSubset(of:records) { return false }
        return sets.count + removed.count + restored.count + entryRemoved.count + entryRestored.count <= 200
    }
}
