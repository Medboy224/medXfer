/// The settings: theme and accent (UI-4), and the developer mode (DEV-13), saved at once.
library;

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';

class SettingsScreen extends StatelessWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final settings = AppScope.of(context).settings;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final names = {
      ThemeChoice.system: l.themeSystem,
      ThemeChoice.light: l.themeLight,
      ThemeChoice.dark: l.themeDark,
      ThemeChoice.black: l.themeBlack,
    };
    Widget heading(String text) => Padding(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 8),
      child: Text(text, style: TextStyle(color: c.muted, fontWeight: FontWeight.w700)),
    );
    return Scaffold(
      appBar: AppBar(title: Text(l.settings)),
      body: ListenableBuilder(
        listenable: settings,
        builder: (context, _) => ListView(
          children: [
            heading(l.theme),
            RadioGroup<ThemeChoice>(
              groupValue: settings.theme,
              onChanged: (t) => settings.theme = t ?? settings.theme,
              child: Column(
                children: [
                  for (final t in ThemeChoice.values)
                    RadioListTile<ThemeChoice>(key: ValueKey('theme-${t.name}'), value: t, title: Text(names[t]!)),
                ],
              ),
            ),
            heading(l.accentColor),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              child: Wrap(
                spacing: 12,
                children: [
                  for (final (i, a) in accents.indexed)
                    InkWell(
                      key: ValueKey('accent-$i'),
                      customBorder: const CircleBorder(),
                      onTap: () => settings.accent = a,
                      child: CircleAvatar(
                        radius: 20,
                        backgroundColor: a,
                        child: a == settings.accent ? const Icon(Icons.check_rounded, color: Colors.white) : null,
                      ),
                    ),
                ],
              ),
            ),
            const SizedBox(height: 12),
            Divider(color: c.border),
            SwitchListTile(
              key: const Key('dev-mode'),
              value: settings.devMode,
              onChanged: (v) => settings.devMode = v,
              title: Text(l.devMode),
              subtitle: Text(l.devModeHint),
            ),
          ],
        ),
      ),
    );
  }
}
