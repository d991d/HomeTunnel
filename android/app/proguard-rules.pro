# Copyright (c) 2026 d991d. All rights reserved.
# HomeTunnel ProGuard rules

# Keep gomobile-generated classes
-keep class go.** { *; }
-keep class mobile.** { *; }

# Keep HomeTunnel service and activity
-keep class io.github.d991d.hometunnel.** { *; }

# Keep VpnService subclasses
-keep public class * extends android.net.VpnService
