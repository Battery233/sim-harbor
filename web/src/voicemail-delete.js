export async function deleteRecording(request, media, confirmed) {
  if (!confirmed || typeof media !== 'string' || !/^[a-f0-9]{32}$/.test(media))
    throw new Error('请确认要删除的这段录音。')
  // One explicit attempt only. A lost response is reconciled by listing again.
  return request('/voicemails/' + media, 'DELETE', { confirmed: true })
}
