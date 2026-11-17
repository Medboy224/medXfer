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
      child: Text(text, style: titleStyle(15)),
    );
    final accentNames = [l.accentVolt, l.accentBlue, l.accentOrange, l.accentViolet];
    return Scaffold(
      appBar: AppBar(title: Text(l.settings)),
      body: ListenableBuilder(
        listenable: settings,
        builder: (context, _) => ListView(
          padding: const EdgeInsets.fromLTRB(0, 0, 0, 24),
          children: [
            heading(l.thisDeviceSection),
            const _Card(child: _DeviceName()),
            heading(l.theme),
            _Card(
              child: RadioGroup<ThemeChoice>(
              groupValue: settings.theme,
              onChanged: (t) => settings.theme = t ?? settings.theme,
              child: Column(
                children: [
                  for (final t in ThemeChoice.values)
                    RadioListTile<ThemeChoice>(key: ValueKey('theme-${t.name}'), value: t, title: Text(names[t]!, style: const TextStyle(fontWeight: FontWeight.w600))),
                ],
              ),
            ),
            ),
            heading(l.accentColor),
            _Card(
              child: Padding(
                padding: const EdgeInsets.all(14),
                child: Wrap(
                  spacing: 14,
                  runSpacing: 10,
                  children: [
                    for (final (i, a) in accents.indexed)
                      Semantics(
                        button: true,
                        selected: a == settings.accent,
                        label: accentNames[i],
                        excludeSemantics: true,
                        child: InkWell(
                          key: ValueKey('accent-$i'),
                          customBorder: const CircleBorder(),
                          onTap: () => settings.accent = a,
                          // The one chosen: a ring and a check, not the color alone.
                          child: Container(
                            padding: const EdgeInsets.all(3),
                            decoration: BoxDecoration(shape: BoxShape.circle, border: Border.all(color: a == settings.accent ? c.text : Colors.transparent, width: 2)),
                            child: CircleAvatar(
                              radius: 22,
                              backgroundColor: a.bright,
                              child: a == settings.accent ? const Icon(Icons.check_rounded, color: Color(0xFF0D0F0E)) : null,
                            ),
                          ),
                        ),
                      ),
                  ],
                ),
              ),
            ),
            const _ReceiveSection(),
            heading(l.devMode),
            _Card(
              child: SwitchListTile(
                key: const Key('dev-mode'),
                value: settings.devMode,
                onChanged: (v) => settings.devMode = v,
                title: Text(l.devMode, style: const TextStyle(fontWeight: FontWeight.w700)),
                subtitle: Text(l.devModeHint),
              ),
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
          padding: const EdgeInsets.fromLTRB(16, 20, 16, 8),
          child: Text(l.receiveTitle, style: titleStyle(15)),
        ),
        _Card(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
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
          ),
        ),
      ],
    );
  }
}

/// A section of the settings, in a card.
class _Card extends StatelessWidget {
  const _Card({required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(horizontal: 12),
    child: Material(color: context.colors.surface, borderRadius: BorderRadius.circular(cardRadius), clipBehavior: Clip.antiAlias, child: child),
  );
}

/// This device's name, and renaming it (plan J8, WP-8.1b).
class _DeviceName extends StatelessWidget {
  const _DeviceName();

  Future<void> _rename(BuildContext context) async {
    final state = AppScope.of(context, listen: false).state;
    final l = AppLocalizations.of(context);
    final messenger = ScaffoldMessenger.of(context);
    final name = await showDialog<String>(context: context, builder: (_) => _RenameDialog(initial: state.deviceName));
    if (name == null || name.trim() == state.deviceName) return;
    final ok = await state.renameDevice(name);
    messenger
      ..clearSnackBars()
      ..showSnackBar(SnackBar(content: Text(ok ? l.deviceNameSaved : l.deviceNameRefused)));
  }

  @override
  Widget build(BuildContext context) {
    final state = AppScope.of(context).state;
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return ListenableBuilder(
      listenable: Listenable.merge([state, state.connection]),
      builder: (context, _) => Padding(
        padding: const EdgeInsets.fromLTRB(12, 12, 12, 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Container(
                  width: 40,
                  height: 40,
                  decoration: BoxDecoration(color: c.section, borderRadius: BorderRadius.circular(12)),
                  child: Icon(state.desktop ? Icons.computer_rounded : Icons.smartphone_rounded, size: 20, color: c.accent),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(l.deviceNameLabel, style: TextStyle(fontSize: 12, color: c.muted)),
                      Text(state.deviceName, key: const Key('device-name'), maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w700)),
                    ],
                  ),
                ),
                OutlinedButton(key: const Key('device-rename'), onPressed: () => unawaited(_rename(context)), child: Text(l.rename)),
              ],
            ),
            if (state.connection.instance.isNotEmpty) ...[
              const SizedBox(height: 8),
              Text(l.deviceNameHint(state.connection.instance), style: TextStyle(fontSize: 12, height: 1.45, color: c.muted)),
            ],
          ],
        ),
      ),
    );
  }
}

/// The new name: 64 characters at most (the core's limit), never empty.
class _RenameDialog extends StatefulWidget {
  const _RenameDialog({required this.initial});

  final String initial;

  @override
  State<_RenameDialog> createState() => _RenameDialogState();
}

class _RenameDialogState extends State<_RenameDialog> {
  late final _field = TextEditingController(text: widget.initial);

  @override
  void dispose() {
    _field.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final empty = _field.text.trim().isEmpty;
    return AlertDialog(
      title: Text(l.deviceNameLabel),
      content: TextField(
        key: const Key('device-name-field'),
        controller: _field,
        autofocus: true,
        maxLength: 64,
        decoration: InputDecoration(errorText: empty ? l.deviceNameEmpty : null),
        onChanged: (_) => setState(() {}),
        onSubmitted: (v) => empty ? null : Navigator.of(context).pop(v),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.of(context).pop(), child: Text(l.cancel)),
        FilledButton(key: const Key('device-name-save'), onPressed: empty ? null : () => Navigator.of(context).pop(_field.text), child: Text(l.save)),
      ],
    );
  }
}
