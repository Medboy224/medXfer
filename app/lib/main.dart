import 'package:flutter/material.dart';

import 'app.dart';
import 'app_state.dart';
import 'core/ffi_core.dart';
import 'platform/folders.dart';
import 'platform/hotspot.dart';
import 'platform/network.dart';
import 'platform/zxing_qr_scanner.dart';
import 'settings.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  final state = AppState(core: FfiCore(), folders: const SystemFolders(), network: systemNetworkInfo(), hotspot: systemHotspot(), qrScanner: systemQrScanner());
  state.start();
  runApp(MedxferApp(state: state, settings: AppSettings()));
}
