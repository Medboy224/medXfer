/// The app: the theme the user picked, the languages, and the screens.
library;

import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';

import 'app_state.dart';
import 'features/home/home_screen.dart';
import 'l10n/app_localizations.dart';
import 'settings.dart';
import 'theme.dart';

/// Gives the screens the shared state and the settings.
class AppScope extends InheritedWidget {
  const AppScope({super.key, required this.state, required this.settings, required super.child});

  final AppState state;
  final AppSettings settings;

  static AppScope of(BuildContext context) => context.dependOnInheritedWidgetOfExactType<AppScope>()!;

  @override
  bool updateShouldNotify(AppScope old) => state != old.state || settings != old.settings;
}

class MedxferApp extends StatelessWidget {
  const MedxferApp({super.key, required this.state, required this.settings});

  final AppState state;
  final AppSettings settings;

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
            home: const HomeScreen(),
          );
        },
      ),
    );
  }
}
