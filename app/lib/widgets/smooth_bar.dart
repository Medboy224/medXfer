/// A progress bar that glides between two updates of the core. The core counts the bytes of the
/// chunks on their way too, not only the chunks written (WP-E.6 of the cable plan: at 1.3 MB/s
/// the bar stood still for seconds, then jumped 44 % at once); gliding at a steady pace towards
/// each new figure, it moves on evenly between them. None of it when the system asks for less
/// motion.
library;

import 'package:flutter/material.dart';

class SmoothBar extends StatelessWidget {
  const SmoothBar({super.key, required this.value, this.minHeight = 8, this.color});

  /// From 0 to 1; null: going on, length unknown.
  final double? value;
  final double minHeight;
  final Color? color;

  /// The time between two figures of the core (200 ms) and a little more: the bar reaches each
  /// one as the next comes, never late behind the transfer (real test of 17 November 2026: at
  /// 800 ms it went on moving once the transfer was over).
  static const glide = Duration(milliseconds: 250);

  @override
  Widget build(BuildContext context) {
    final radius = BorderRadius.circular(minHeight / 2);
    final v = value;
    if (v == null) return LinearProgressIndicator(minHeight: minHeight, borderRadius: radius, color: color);
    return TweenAnimationBuilder<double>(
      tween: Tween(end: v),
      // The end is shown at once: never a bar still moving under « done ».
      duration: MediaQuery.of(context).disableAnimations || v >= 1 ? Duration.zero : glide,
      curve: Curves.linear,
      builder: (context, shown, _) => LinearProgressIndicator(value: shown, minHeight: minHeight, borderRadius: radius, color: color),
    );
  }
}
