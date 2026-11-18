import Foundation

@main struct Main {
    static func main() throws {
        func object(_ raw: String) throws -> [String:Any] { try JSONSerialization.jsonObject(with: Data(raw.utf8)) as! [String:Any] }
        func check(_ name:String,_ value:Bool) { if !value { fatalError(name) } }
        let event = "event-0001"
        let create = try object(#"{"source":"free","date":"2026-10-28","timeZone":"UTC","clientEventId":"event-0001","mode":"after_workout"}"#)
        check("create",NeraeRecordingV2JournalValidator.validate(path:"/me/recordings",body:create,event:event))
        let batch = try object(#"{"expectedVersion":1,"exerciseEntryKeys":["entry-0001"],"addEntries":[{"exerciseEntryKey":"entry-0001","exerciseName":"深蹲"}],"sets":[{"recordKey":"record-001","exerciseEntryKey":"entry-0001","setIndex":1,"confirmed":false,"measurement":{"method":"total"}}],"removeRecords":[{"recordKey":"record-001","removedPosition":1}]}"#)
        check("batch",NeraeRecordingV2JournalValidator.validate(path:"/me/recordings/42/sets",body:batch,event:event))
        let date = try object(#"{"expectedVersion":1,"date":"2026-02-28","exerciseEntryKeys":[]}"#)
        check("date",NeraeRecordingV2JournalValidator.validate(path:"/me/recordings/42/sets",body:date,event:event))
        let invalidDate = try object(#"{"expectedVersion":1,"date":"2026-02-30","exerciseEntryKeys":[]}"#)
        check("invalid date",!NeraeRecordingV2JournalValidator.validate(path:"/me/recordings/42/sets",body:invalidDate,event:event))
        let bad = try object(#"{"expectedVersion":1,"exerciseEntryKeys":["entry-0001"],"sets":[{"recordKey":"record-001","exerciseEntryKey":"entry-0001","setIndex":1,"confirmed":false,"measurement":{"method":"total","weightKg":-1}}]}"#)
        check("bad weight",!NeraeRecordingV2JournalValidator.validate(path:"/me/recordings/42/sets",body:bad,event:event))
        let mismatched = try object(#"{"source":"free","date":"2026-10-28","clientEventId":"other-0001","mode":"after_workout"}"#)
        check("mismatched event",!NeraeRecordingV2JournalValidator.validate(path:"/me/recordings",body:mismatched,event:event))
        let finish = try object(#"{"expectedVersion":2}"#)
        check("finish",NeraeRecordingV2JournalValidator.validate(path:"/me/recordings/42/finish",body:finish,event:event))
        print("Swift V2 journal validation: 7 cases passed")
    }
}
