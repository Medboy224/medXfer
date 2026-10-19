import 'package:flutter/material.dart';

import 'app.dart';
import 'app_state.dart';
import 'core/ffi_core.dart';
import 'platform/folders.dart';
import 'settings.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  final state = AppState(core: FfiCore(), folders: const SystemFolders());
  state.start();
  runApp(MedxferApp(state: state, settings: AppSettings()));
}
