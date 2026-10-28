import 'dart:io';

import 'package:flutter/material.dart';

import 'app.dart';
import 'app_state.dart';
import 'core/ffi_core.dart';
import 'perf.dart';
import 'platform/background.dart';
import 'platform/file_picker.dart';
import 'platform/folders.dart';
import 'platform/library.dart';
import 'platform/hotspot.dart';
import 'platform/network.dart';
import 'platform/open_folder.dart';
import 'platform/storage.dart';
import 'platform/zxing_qr_scanner.dart';
import 'settings.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  FrameStats.start();
  // Read before the first frame: the saved theme from the start, without a flash.
  final settings = AppSettings(store: FileSettingsStore(const SystemFolders().dataDir));
  await settings.load();
  final state = AppState(
    settings: settings,
    core: FfiCore(),
    folders: const SystemFolders(),
    network: systemNetworkInfo(),
    hotspot: systemHotspot(),
    qrScanner: systemQrScanner(),
    filePicker: systemFilePicker(),
    folderOpener: systemFolderOpener(),
    storage: systemReceiveStorage(),
    background: systemBackground(),
    library: systemLibrary(),
    desktop: Platform.isWindows || Platform.isLinux || Platform.isMacOS,
  );
  state.start();
  runApp(MedxferApp(state: state, settings: settings));
}
