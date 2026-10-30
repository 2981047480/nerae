import {
  compareAndSetTrainingOperationJournal,
  readTrainingOperationJournal,
} from '@/uni_modules/nerae-secure-vault'
import { SecureOperationJournalStorage } from '../../features/training/secureOperationJournalStorage'

/** Creates the TrainingOperationJournal storage adapter for the fixed native vault slot. */
export function createNativeTrainingJournalStorage(): SecureOperationJournalStorage {
  return new SecureOperationJournalStorage({
    readTrainingOperationJournal,
    compareAndSetTrainingOperationJournal,
  })
}
