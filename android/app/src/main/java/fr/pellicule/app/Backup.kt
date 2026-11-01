package fr.pellicule.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.pm.ServiceInfo
import android.os.Build
import android.provider.MediaStore
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.withContext
import java.io.IOException
import java.net.ConnectException
import java.net.NoRouteToHostException
import java.net.SocketTimeoutException
import java.util.concurrent.TimeUnit

/** Planification de la sauvegarde : toutes les 15 min en Wi-Fi + dès qu'une photo apparaît. */
object Backup {
    private const val PERIODIC = "periodic"
    private const val ON_NEW_MEDIA = "on-new-media"
    private const val NOW = "now"

    private fun wifi() = Constraints.Builder().setRequiredNetworkType(NetworkType.UNMETERED).build()

    fun schedule(ctx: Context) {
        val wm = WorkManager.getInstance(ctx)
        wm.enqueueUniquePeriodicWork(
            PERIODIC, ExistingPeriodicWorkPolicy.UPDATE,
            PeriodicWorkRequestBuilder<BackupWorker>(15, TimeUnit.MINUTES).setConstraints(wifi()).build()
        )
        watchNewMedia(ctx, ExistingWorkPolicy.KEEP)
    }

    fun watchNewMedia(ctx: Context, policy: ExistingWorkPolicy) {
        val c = Constraints.Builder()
            .setRequiredNetworkType(NetworkType.UNMETERED)
            .addContentUriTrigger(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, true)
            .addContentUriTrigger(MediaStore.Video.Media.EXTERNAL_CONTENT_URI, true)
            .setTriggerContentUpdateDelay(30, TimeUnit.SECONDS)
            .build()
        WorkManager.getInstance(ctx).enqueueUniqueWork(
            ON_NEW_MEDIA, policy,
            OneTimeWorkRequestBuilder<BackupWorker>().setConstraints(c).setInputData(workDataOf("rewatch" to true)).build()
        )
    }

    /** Lancement manuel : tout réseau, le serveur doit simplement répondre. */
    fun runNow(ctx: Context) {
        val c = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()
        WorkManager.getInstance(ctx).enqueueUniqueWork(
            NOW, ExistingWorkPolicy.KEEP, OneTimeWorkRequestBuilder<BackupWorker>().setConstraints(c).build()
        )
    }

    fun cancel(ctx: Context) {
        val wm = WorkManager.getInstance(ctx)
        listOf(PERIODIC, ON_NEW_MEDIA, NOW).forEach { wm.cancelUniqueWork(it) }
    }
}

class BackupWorker(ctx: Context, params: WorkerParameters) : CoroutineWorker(ctx, params) {
    companion object {
        private val lock = Mutex() // une seule sauvegarde à la fois
        private const val CHANNEL = "backup"
        private const val NOTIF_ID = 42
    }

    private val prefs = Prefs(ctx)

    override suspend fun doWork(): Result {
        if (inputData.getBoolean("rewatch", false)) {
            Backup.watchNewMedia(applicationContext, ExistingWorkPolicy.APPEND_OR_REPLACE)
        }
        if (!prefs.configured || !prefs.backupEnabled) return Result.success()
        if (!lock.tryLock()) return Result.success()
        prefs.running = true
        try {
            withContext(Dispatchers.IO) { run() }
        } finally {
            prefs.running = false
            prefs.progressDone = 0
            prefs.progressTotal = 0
            prefs.lastRun = System.currentTimeMillis()
            lock.unlock()
        }
        return Result.success()
    }

    private suspend fun run() {
        val ctx = applicationContext
        if (!Media.hasPermission(ctx)) {
            prefs.lastResult = "accès aux photos non autorisé"
            return
        }
        when (Api.reach(prefs.server, prefs.token)) {
            Api.Reach.UNREACHABLE -> { prefs.lastResult = "serveur injoignable (hors de la maison ?)"; return }
            Api.Reach.UNAUTHORIZED -> { prefs.lastResult = "session expirée : reconnecte-toi dans les réglages"; return }
            Api.Reach.OK -> {}
        }

        val db = UploadedDb(ctx)
        val done = db.doneIds()
        var todo = Media.toBackup(ctx, prefs).filter { done[it.id] != it.size }

        // ce qui est déjà sur le serveur (envoyé à la main, par FolderSync…) n'est pas renvoyé
        if (todo.isNotEmpty()) {
            val existing = HashSet<String>()
            todo.chunked(500).forEach { existing += Api.check(prefs.server, prefs.token, it.map { m -> m.key }) }
            todo.filter { it.key in existing }.forEach { db.mark(it) }
            todo = todo.filter { it.key !in existing }
        }
        if (todo.isEmpty()) {
            prefs.lastResult = "tout est sauvegardé"
            return
        }

        val foreground = tryForeground("Sauvegarde de ${todo.size} élément(s)…", 0, todo.size)
        var sent = 0
        var errors = 0
        var lastError = ""
        prefs.progressTotal = todo.size
        for ((i, item) in todo.withIndex()) {
            if (isStopped) break
            prefs.progressDone = i
            if (foreground) tryForeground("Sauvegarde ${i + 1} / ${todo.size}", i, todo.size)
            try {
                var uri = item.uri
                if (Build.VERSION.SDK_INT >= 29 && Media.canReadLocation(ctx)) {
                    uri = MediaStore.setRequireOriginal(uri) // garde la position GPS dans le fichier
                }
                val input = ctx.contentResolver.openInputStream(uri) ?: throw IOException("fichier illisible")
                input.use { Api.upload(prefs.server, prefs.token, item, it) }
                db.mark(item)
                sent++
            } catch (e: Exception) {
                if (e is ConnectException || e is SocketTimeoutException || e is NoRouteToHostException) {
                    lastError = "connexion perdue"
                    break // plus de réseau / sorti de la maison : on reprendra plus tard
                }
                if (e is Api.HttpError && e.code == 401) {
                    lastError = "session expirée"
                    break
                }
                errors++
                lastError = e.message ?: e.javaClass.simpleName
            }
        }
        prefs.lastResult = buildString {
            append("$sent envoyé(s)")
            val left = todo.size - sent - errors
            if (left > 0) append(", $left restant(s)")
            if (errors > 0) append(", $errors erreur(s)")
            if (lastError.isNotEmpty()) append(" — $lastError")
        }
    }

    private fun notification(text: String, progress: Int, max: Int): Notification {
        val nm = applicationContext.getSystemService(NotificationManager::class.java)
        if (nm.getNotificationChannel(CHANNEL) == null) {
            nm.createNotificationChannel(NotificationChannel(CHANNEL, "Sauvegarde", NotificationManager.IMPORTANCE_LOW))
        }
        return Notification.Builder(applicationContext, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat)
            .setContentTitle("Pellicule")
            .setContentText(text)
            .setProgress(max, progress, max == 0)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .build()
    }

    /** Passe en service de premier plan (pas de limite de 10 min). Refusé par Android si l'app est en arrière-plan : on continue quand même. */
    private suspend fun tryForeground(text: String, progress: Int, max: Int): Boolean = try {
        val n = notification(text, progress, max)
        setForeground(
            if (Build.VERSION.SDK_INT >= 29) ForegroundInfo(NOTIF_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
            else ForegroundInfo(NOTIF_ID, n)
        )
        true
    } catch (e: Exception) {
        false
    }
}
