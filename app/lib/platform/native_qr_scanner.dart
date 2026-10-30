/// Android's adapter of [QrScanner]: the app's own camera screen (QrScanActivity.kt), whose
/// reader is zxing-cpp (Apache 2.0), its own Android library. It replaced flutter_zxing: with it,
/// the same reader found the code but misread it, up to a minute without a reading, its camera's
/// pipeline at fault; on this screen it reads in 0.4 to 1 s, near and far (decision C8 of the J6c
/// plan, #227). Another reader would replace the Kotlin decoder alone.
library;

import 'dart:io';

import 'package:flutter/widgets.dart';

import '../l10n/app_localizations.dart';
import 'folders.dart' show platformChannel;
import 'qr_scanner.dart';

/// Each scan measured in the log (logcat, tag medxfer-scan) with
/// --dart-define=MEDXFER_LOG_EVENTS=true: how long from the camera opening to the reading, over
/// how many pictures.
const _logScans = bool.fromEnvironment('MEDXFER_LOG_EVENTS');

class NativeQrScanner implements QrScanner {
  const NativeQrScanner();

  @override
  bool get supported => Platform.isAndroid;

  @override
  Future<String?> scan(BuildContext context) {
    final l = AppLocalizations.of(context);
    return platformChannel.invokeMethod<String>('scanQr', {'title': l.scanQr, 'hint': l.scanHint, 'log': _logScans});
  }
}

QrScanner systemQrScanner() => Platform.isAndroid ? const NativeQrScanner() : const NoQrScanner();
