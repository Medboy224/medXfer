import 'package:flutter/material.dart';

import 'app.dart';
import 'app_state.dart';
import 'core/ffi_core.dart';
import 'platform/folders.dart';
import 'platform/network.dart';
import 'settings.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  final state = AppState(core: FfiCore(), folders: const SystemFolders(), network: systemNetworkInfo());
  state.start();
  runApp(MedxferApp(state: state, settings: AppSettings()));
}
