import {
  compareAndSetTrainingOperationJournal,
  compareAndSetVersionedTrainingOperationJournal,
  claimTrainingOperationDispatch,
  readTrainingOperationJournal,
  readVersionedTrainingOperationJournal,
} from '@/uni_modules/nerae-secure-vault'
import {
  SecureOperationJournalStorage,
  type JournalAuth,
} from '../../features/training/secureOperationJournalStorage'

/** Creates an adapter bound to this authenticated subject and session generation. */
export function createNativeTrainingJournalStorage(auth: JournalAuth): SecureOperationJournalStorage {
  return new SecureOperationJournalStorage({
    readTrainingOperationJournal,
    compareAndSetTrainingOperationJournal,
    readVersionedTrainingOperationJournal,
    compareAndSetVersionedTrainingOperationJournal,
    claimTrainingOperationDispatch,
  }, auth)
}
