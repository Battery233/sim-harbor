// Match maintenance format limits. Browser memory downloads are separately bounded.
export const databaseArchiveLimit = 544 * 1024 * 1024
export const maxBackupArchive = (65536 + 544) * 1024 * 1024
export const maxBrowserBuffer = 256 * 1024 * 1024
