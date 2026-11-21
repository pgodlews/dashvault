`camera.mp4` is a synthetic 30-second blue frame sequence, with no recorded footage or audio. Recreate it with:

```bash
ffmpeg -f lavfi -i color=c=blue:s=160x90:r=5 -t 30 -an \
  -c:v libx264 -pix_fmt yuv420p -movflags +faststart camera.mp4
```

Browser tests serve it with HTTP byte-range support so native seek behavior is exercised.
