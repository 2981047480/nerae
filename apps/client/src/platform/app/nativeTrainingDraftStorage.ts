import { createTrainingDraft, discoverTrainingDrafts, readTrainingDraft, writeTrainingDraft } from '@/uni_modules/nerae-secure-vault'
import type { AuthLease } from '../../features/training/draftStorage'
import { SecureDraftStorage } from '../../features/training/secureDraftStorage'

/** Fail closed when a platform has not provided the protected native slot. */
export function createNativeTrainingDraftStorage(subjectId: string, auth: AuthLease): SecureDraftStorage {
  return new SecureDraftStorage({ discoverTrainingDrafts, readTrainingDraft, createTrainingDraft, writeTrainingDraft }, subjectId, auth)
}
