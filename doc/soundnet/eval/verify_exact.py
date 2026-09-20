"""Is the fused ONNX export faithful to the PyTorch model it came from?

The comparison against sherpa-onnx conflates two questions: whether the export
is correct, and whether kaldi fbank and torchaudio's MelSpectrogram agree. They
do not agree exactly, and CED was trained with the torchaudio one, so sherpa is
the approximation - which means a disagreement there proves nothing about the
export.

This compares like with like: the same weights and the same front-end, in
PyTorch and in ONNX Runtime. Any difference here is the export's fault and
should be at the level of float32 arithmetic reordering.
"""
import glob
import os
import sys
import wave

sys.path.insert(0, "/root/gotmp/ced-src")

import numpy as np
import onnxruntime as ort
import torch
import torch.nn as nn
import torchaudio
import torchaudio.transforms as aut

import models

SINGLE = "/root/gotmp/models/ced_tiny_fused_single.onnx"
WINDOW = 48000


class FusedCED(nn.Module):
    def __init__(self, model):
        super().__init__()
        self.mel = aut.MelSpectrogram(
            f_min=0, sample_rate=16000, win_length=512, center=False,
            n_fft=512, f_max=8000, hop_length=160, n_mels=64)
        self.to_db = aut.AmplitudeToDB(top_db=120)
        self.model = model

    def forward(self, waveform):
        return self.model.forward_spectrogram(self.to_db(self.mel(waveform)))


def read_window(path):
    with wave.open(path, "rb") as w:
        n, sw, ch, sr = w.getnframes(), w.getsampwidth(), w.getnchannels(), w.getframerate()
        raw = w.readframes(n)
    a = np.frombuffer(raw[: (len(raw) // 2) * 2], dtype="<i2").astype(np.float32) / 32768.0
    if ch > 1:
        a = a.reshape(-1, ch).mean(axis=1)
    wav = torch.from_numpy(a.copy())
    if sr != 16000:
        wav = torchaudio.functional.resample(wav, sr, 16000)
    if wav.numel() < WINDOW:
        wav = torch.nn.functional.pad(wav, (0, WINDOW - wav.numel()))
    s = max(0, (wav.numel() - WINDOW) // 2)
    return wav[s:s + WINDOW]


torch.set_grad_enabled(False)
base = getattr(models, "ced_tiny")(target_length=1012, pretrained=True).eval()
ref = FusedCED(base).eval()

sess = ort.InferenceSession(SINGLE, providers=["CPUExecutionProvider"])
inp = sess.get_inputs()[0].name

clips = sys.argv[1:] or sorted(glob.glob("/root/gotmp/clips/*.wav"))[:12]
worst = 0.0
worst_clip = ""
for path in clips:
    win = read_window(path)
    a = ref(win.unsqueeze(0))[0].numpy()
    b = sess.run(None, {inp: win.unsqueeze(0).numpy().astype(np.float32)})[0][0]
    d = float(np.max(np.abs(a - b)))
    if d > worst:
        worst, worst_clip = d, os.path.basename(path)
    print(f"{os.path.basename(path):<48} max|torch-onnx| = {d:.2e}  "
          f"argmax torch={int(np.argmax(a)):3d} onnx={int(np.argmax(b)):3d}")

print()
print(f"worst absolute difference across {len(clips)} windows: {worst:.2e} ({worst_clip})")
print("float32 arithmetic noise is ~1e-6; anything above ~1e-3 means the export is wrong")
