/// A progress bar that glides between two updates of the core. The progress moves a chunk at a
/// time (2 MB): on a slow link the bar jumped every second, then stood still. Gliding at a
/// steady pace towards each new figure, it moves on evenly. None of it when the system asks for
/// less motion.
library;

import 'package:flutter/material.dart';

class SmoothBar extends StatelessWidget {
  const SmoothBar({super.key, required this.value, this.minHeight = 8, this.color});

  /// From 0 to 1; null: going on, length unknown.
  final double? value;
  final double minHeight;
  final Color? color;

  /// About the time between two chunks on a slow link: the bar reaches each figure as the
  /// next one comes.
  static const glide = Duration(milliseconds: 800);

  @override
  Widget build(BuildContext context) {
    final radius = BorderRadius.circular(minHeight / 2);
    final v = value;
    if (v == null) return LinearProgressIndicator(minHeight: minHeight, borderRadius: radius, color: color);
    return TweenAnimationBuilder<double>(
      tween: Tween(end: v),
      duration: MediaQuery.of(context).disableAnimations ? Duration.zero : glide,
      curve: Curves.linear,
      builder: (context, shown, _) => LinearProgressIndicator(value: shown, minHeight: minHeight, borderRadius: radius, color: color),
    );
  }
}
