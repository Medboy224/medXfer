/// A folder of the phone's storage, chosen for received files (WP-B.10): the explorer's listing
/// (Library.listDir), folders only. The desktops use the system's picker instead.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../l10n/app_localizations.dart';
import '../../platform/library.dart';
import '../../theme.dart';

/// The folder chosen, or null.
Future<String?> pickPhoneFolder(BuildContext context, {String? start}) =>
    Navigator.of(context).push<String>(MaterialPageRoute(builder: (_) => FolderPicker(start: start)));

class FolderPicker extends StatefulWidget {
  const FolderPicker({super.key, this.start});

  final String? start;

  @override
  State<FolderPicker> createState() => _FolderPickerState();
}

class _FolderPickerState extends State<FolderPicker> {
  late final Library _library = AppScope.of(context, listen: false).state.library;
  late String _dir;
  List<FileEntry>? _folders;

  @override
  void initState() {
    super.initState();
    _dir = _library.storageRoot;
    final start = widget.start;
    if (start != null && start.startsWith(_library.storageRoot)) _dir = start;
    unawaited(_open(_dir));
  }

  Future<void> _open(String dir) async {
    setState(() {
      _dir = dir;
      _folders = null;
    });
    List<FileEntry> entries;
    try {
      entries = await _library.listDir(dir);
    } on Object {
      entries = const [];
    }
    if (!mounted || dir != _dir) return;
    setState(() => _folders = [for (final e in entries) if (e.dir && !e.name.startsWith('.')) e]);
  }

  String? get _parent {
    if (_dir == _library.storageRoot) return null;
    final i = _dir.lastIndexOf('/');
    return i <= 0 ? null : _dir.substring(0, i);
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final folders = _folders;
    final parent = _parent;
    final shown = _dir.replaceFirst(_library.storageRoot, '').replaceAll('/', ' › ');
    return Scaffold(
      appBar: AppBar(title: Text(l.folderPickerTitle)),
      body: Column(
        children: [
          ListTile(
            leading: IconButton(
              tooltip: l.upFolder,
              icon: const Icon(Icons.arrow_upward_rounded),
              onPressed: parent == null ? null : () => unawaited(_open(parent)),
            ),
            title: Text(shown.isEmpty ? '/' : shown, maxLines: 2, overflow: TextOverflow.ellipsis, style: TextStyle(color: c.muted)),
          ),
          Expanded(
            child: folders == null
                ? const Center(child: CircularProgressIndicator())
                : folders.isEmpty
                ? Center(child: Text(l.folderPickerEmpty, style: TextStyle(color: c.muted)))
                : ListView.builder(
                    key: ValueKey('folders:$_dir'), // a new folder opens at its top
                    itemCount: folders.length,
                    itemBuilder: (context, i) => ListTile(
                      leading: Icon(Icons.folder_rounded, color: Theme.of(context).colorScheme.primary),
                      title: Text(folders[i].name, maxLines: 1, overflow: TextOverflow.ellipsis),
                      onTap: () => unawaited(_open(folders[i].path)),
                    ),
                  ),
          ),
          SafeArea(
            top: false,
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: FilledButton.icon(
                key: const Key('folder-use'),
                style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(52)),
                onPressed: () => Navigator.of(context).pop(_dir),
                icon: const Icon(Icons.check_rounded),
                label: Text(l.folderPickerUse),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
