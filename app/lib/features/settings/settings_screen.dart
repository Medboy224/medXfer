/// The settings: theme and accent (UI-4), where received files go (WP-B.10), and the developer
/// mode (DEV-13), saved at once.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../app_state.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import 'folder_picker.dart';

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
            const _ReceiveSection(),
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

/// Where received files go: the folder chosen, or the default one; on Android, sorted by type or
/// not, and the access to all files a folder of one's choice needs (WP-B.10).
class _ReceiveSection extends StatefulWidget {
  const _ReceiveSection();

  @override
  State<_ReceiveSection> createState() => _ReceiveSectionState();
}

class _ReceiveSectionState extends State<_ReceiveSection> {
  late final AppState _state = AppScope.of(context, listen: false).state;
  bool? _canChoose;
  String _default = '';
  String _gone = '';

  @override
  void initState() {
    super.initState();
    _state.settings.addListener(_changed);
    unawaited(_refresh());
  }

  @override
  void dispose() {
    _state.settings.removeListener(_changed);
    super.dispose();
  }

  void _changed() => unawaited(_refresh());

  Future<void> _refresh() async {
    final folders = _state.receiveFolders;
    final can = await folders.canChoose();
    final def = await folders.defaultDir();
    final place = await folders.placeFor(null);
    if (!mounted) return;
    setState(() {
      _canChoose = can;
      _default = def;
      _gone = place.gone;
    });
  }

  Future<void> _choose() async {
    final chosen = _state.settings.receiveDir;
    final dir = _state.storage.supported
        ? await pickPhoneFolder(context, start: chosen.isEmpty ? _default : chosen)
        : await _state.filePicker.folder();
    if (dir == null || !mounted) return;
    if (!await _state.receiveFolders.writable(dir)) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
        ..clearSnackBars()
        ..showSnackBar(SnackBar(content: Text(AppLocalizations.of(context).receiveFolderNotWritable)));
      return;
    }
    _state.settings.receiveDir = dir;
  }

  Future<void> _grant() async {
    await _state.storage.askAllFiles();
    await _refresh();
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final settings = _state.settings;
    final chosen = settings.receiveDir;
    final can = _canChoose;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
          child: Text(l.receiveTitle, style: TextStyle(color: c.muted, fontWeight: FontWeight.w700)),
        ),
        if (can == null)
          const Padding(padding: EdgeInsets.all(16), child: LinearProgressIndicator())
        else if (!can)
          // Android without the access to all files: only Download/medXfer/ is writable.
          ListTile(
            key: const Key('receive-folder'),
            title: Text(l.receiveFolder),
            subtitle: Text(l.receiveNeedsAccess(_default)),
            trailing: TextButton(onPressed: () => unawaited(_grant()), child: Text(l.grantAccess)),
          )
        else ...[
          ListTile(
            key: const Key('receive-folder'),
            title: Text(l.receiveFolder),
            subtitle: Text(chosen.isEmpty ? l.receiveFolderDefault(_default) : chosen),
            trailing: FilledButton.tonal(key: const Key('receive-choose'), onPressed: () => unawaited(_choose()), child: Text(l.receiveFolderChoose)),
          ),
          if (_gone.isNotEmpty)
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 4),
              child: Text(l.noticeReceiveFolderGone(_gone, _default), style: TextStyle(color: c.danger, fontSize: 13)),
            ),
          if (chosen.isNotEmpty)
            Align(
              alignment: Alignment.centerLeft,
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 8),
                child: TextButton(key: const Key('receive-reset'), onPressed: () => settings.receiveDir = '', child: Text(l.receiveFolderReset)),
              ),
            ),
        ],
        if (_state.receiveFolders.sorts)
          SwitchListTile(
            key: const Key('sort-by-type'),
            value: settings.sortByType,
            onChanged: (v) => settings.sortByType = v,
            title: Text(l.sortByType),
            subtitle: Text(l.sortByTypeHint),
          ),
      ],
    );
  }
}
