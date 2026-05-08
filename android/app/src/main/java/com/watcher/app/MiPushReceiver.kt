package com.watcher.app

import android.content.Context
import android.content.Intent
import android.util.Log

/**
 * MiPush receiver stub — temporarily disabled (maven.xiaomi.net unreachable).
 *
 * TODO: Re-enable when MiPush SDK dependency is restored.
 * Original class extended PushMessageReceiver from com.xiaomi.mipush.sdk.
 */
class MiPushReceiver {

    companion object {
        private const val TAG = "MiPushReceiver"
    }

    // MiPush functionality temporarily disabled.
    // Original implementation handled:
    //   - onReceivePassThroughMessage → triggers BackgroundSyncWorker
    //   - onReceiveRegisterResult → stores regId, registers with relay
    //   - onNotificationMessageClicked → opens MainActivity
}
