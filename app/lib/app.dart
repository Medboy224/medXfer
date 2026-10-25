/// The app: the theme the user picked, the languages, and the screens.
library;

import 'dart:ui' show AppExitResponse;

import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';

import 'app_state.dart';
import 'features/connect/connection_prompts.dart';
import 'features/dev/dev_banner.dart';
import 'features/transfer/transfer_prompts.dart';
import 'features/home/home_screen.dart';
import 'l10n/app_localizations.dart';
import 'settings.dart';
import 'theme.dart';

/// Gives the screens the shared state and the settings.
class AppScope extends InheritedWidget {
  const AppScope({super.key, required this.state, required this.settings, required super.child});

  final AppState state;
  final AppSettings settings;

  /// listen: false in initState, or where a change of scope must not rebuild.
  static AppScope of(BuildContext context, {bool listen = true}) => listen
      ? context.dependOnInheritedWidgetOfExactType<AppScope>()!
      : context.getInheritedWidgetOfExactType<AppScope>()!;

  @override
  bool updateShouldNotify(AppScope old) => state != old.state || settings != old.settings;
}

class MedxferApp extends StatefulWidget {
  const MedxferApp({super.key, required this.state, required this.settings});

  final AppState state;
  final AppSettings settings;

  @override
  State<MedxferApp> createState() => _MedxferAppState();
}

class _MedxferAppState extends State<MedxferApp> {
  // What the other device asks of this user opens above any screen (ConnectionPrompts).
  final _navigator = GlobalKey<NavigatorState>();
  final _messenger = GlobalKey<ScaffoldMessengerState>();

  AppState get state => widget.state;
  AppSettings get settings => widget.settings;

  late final AppLifecycleListener _lifecycle;

  @override
  void initState() {
    super.initState();
    // Closing the window stops the core first (AppState.shutdown).
    _lifecycle = AppLifecycleListener(
      onExitRequested: () async {
        await state.shutdown();
        return AppExitResponse.exit;
      },
    );
  }

  @override
  void dispose() {
    _lifecycle.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AppScope(
      state: state,
      settings: settings,
      child: ListenableBuilder(
        listenable: settings,
        builder: (context, _) {
          final dark = settings.theme == ThemeChoice.black ? MedxferColors.black : MedxferColors.dark;
          final mode = switch (settings.theme) {
            ThemeChoice.system => ThemeMode.system,
            ThemeChoice.light => ThemeMode.light,
            ThemeChoice.dark || ThemeChoice.black => ThemeMode.dark,
          };
          return MaterialApp(
            onGenerateTitle: (context) => AppLocalizations.of(context).appTitle,
            debugShowCheckedModeBanner: false,
            theme: buildTheme(MedxferColors.light, settings.accent, Brightness.light),
            darkTheme: buildTheme(dark, settings.accent, Brightness.dark),
            themeMode: mode,
            localizationsDelegates: const [
              AppLocalizations.delegate,
              GlobalMaterialLocalizations.delegate,
              GlobalWidgetsLocalizations.delegate,
              GlobalCupertinoLocalizations.delegate,
            ],
            supportedLocales: AppLocalizations.supportedLocales,
            navigatorKey: _navigator,
            scaffoldMessengerKey: _messenger,
            builder: (context, child) => DevBanner(
              settings: settings,
              navigator: _navigator,
              child: ConnectionPrompts(
                connection: state.connection,
                navigator: _navigator,
                messenger: _messenger,
                child: TransferPrompts(
                  transfers: state.transfers,
                  navigator: _navigator,
                  messenger: _messenger,
                  child: child ?? const SizedBox.shrink(),
                ),
              ),
            ),
            home: const HomeScreen(),
          );
        },
      ),
    );
  }
}
