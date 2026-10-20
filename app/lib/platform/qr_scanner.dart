/// Reading a QR code with the camera (decision J5): an interface, and one adapter per library.
/// The screens know only [QrScanner]; zxing_qr_scanner.dart is flutter_zxing's adapter.
library;

import 'package:flutter/widgets.dart';

abstract interface class QrScanner {
  /// A camera to scan with: Android. The desktops paste the link instead.
  bool get supported;

  /// Opens the camera until a medXfer link is read; null when the user goes back.
  Future<String?> scan(BuildContext context);
}

/// No camera: the link is pasted.
class NoQrScanner implements QrScanner {
  const NoQrScanner();

  @override
  bool get supported => false;

  @override
  Future<String?> scan(BuildContext context) async => null;
}
