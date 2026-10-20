/// Files and folders dropped on the window, by path (desktops). The desktop_drop package
/// (Apache 2.0) is the adapter: this widget is its only user.
library;

import 'dart:io';

import 'package:desktop_drop/desktop_drop.dart';
import 'package:flutter/material.dart';

class DropZone extends StatefulWidget {
  const DropZone({super.key, required this.onPaths, required this.child, this.enabled = true, this.hint = ''});

  final ValueChanged<List<String>> onPaths;
  final Widget child;
  final bool enabled;

  /// Shown over the window while something is dragged over it.
  final String hint;

  /// A desktop: Android has no drop from another application here.
  static bool get supported => Platform.isWindows || Platform.isLinux || Platform.isMacOS;

  @override
  State<DropZone> createState() => _DropZoneState();
}

class _DropZoneState extends State<DropZone> {
  bool _over = false;

  @override
  Widget build(BuildContext context) {
    if (!DropZone.supported) return widget.child;
    final accent = Theme.of(context).colorScheme.primary;
    return DropTarget(
      enable: widget.enabled,
      onDragEntered: (_) => setState(() => _over = true),
      onDragExited: (_) => setState(() => _over = false),
      onDragDone: (d) {
        setState(() => _over = false);
        // A folder comes whole, by its own path: the core walks it.
        final paths = [
          for (final f in d.files)
            if (f.path.isNotEmpty) f.path,
        ];
        if (paths.isNotEmpty) widget.onPaths(paths);
      },
      child: Stack(
        children: [
          widget.child,
          if (_over)
            Positioned.fill(
              child: IgnorePointer(
                child: Container(
                  decoration: BoxDecoration(
                    color: accent.withValues(alpha: 0.10),
                    border: Border.all(color: accent, width: 3),
                  ),
                  alignment: Alignment.center,
                  child: Text(
                    widget.hint,
                    style: TextStyle(color: accent, fontWeight: FontWeight.w800, fontSize: 20),
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}
