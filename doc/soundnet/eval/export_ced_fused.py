"""Export CED with its mel front-end fused into the ONNX graph.

Every model in soundnet-go hands raw audio to the graph: BirdNET v3's mel is a
Conv1d inside the model, v2.4 uses a truncated DFT, BSG ships "fused". The
stock CED export takes ``feats`` - a (1, 64, frames) log-mel tensor - and leaves
the front-end to the caller, which is why sherpa-onnx reimplements it in C++
with kaldi fbank.

Reimplementing it in Go would mean matching eight parameters by inference, and a
subtly wrong one produces a model that loads, runs and returns plausible
nonsense. Fusing removes the question: whatever the front-end is, the export
captures it exactly.

The front-end is copied verbatim from the upstream repo's
onnx_inference_with_torchaudio.py so there is one source of truth for it. Note
it is a torchaudio MelSpectrogram and *not* kaldi fbank - sherpa-onnx's
kaldi-based feature extractor is an approximation of this, not the original.
"""
import argparse
import sys

sys.path.insert(0, "/root/gotmp/ced-src")

import torch  # noqa: E402
import torch.nn as nn  # noqa: E402
import torchaudio.transforms as aut  # noqa: E402

import models  # noqa: E402


class FusedCED(nn.Module):
    """Raw 16 kHz mono waveform in, 527 AudioSet probabilities out."""

    def __init__(self, model):
        super().__init__()
        self.mel = aut.MelSpectrogram(
            f_min=0,
            sample_rate=16000,
            win_length=512,
            center=False,
            n_fft=512,
            f_max=8000,
            hop_length=160,
            n_mels=64,
        )
        self.to_db = aut.AmplitudeToDB(top_db=120)
        self.model = model

    def forward(self, waveform):
        # waveform: (batch, samples) float32 in [-1, 1]
        x = self.mel(waveform)
        x = self.to_db(x)
        return self.model.forward_spectrogram(x)


@torch.no_grad()
def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", default="ced_tiny")
    ap.add_argument("--max-frames", type=int, default=1012)
    ap.add_argument("--seconds", type=float, default=3.0,
                    help="window the export is traced with; the time axis stays dynamic")
    ap.add_argument("--out", default="/root/gotmp/models/ced_tiny_fused.onnx")
    args = ap.parse_args()

    base = getattr(models, args.model)(target_length=args.max_frames, pretrained=True)
    base = base.eval()
    fused = FusedCED(base).eval()

    samples = int(args.seconds * 16000)
    dummy = torch.zeros(1, samples, dtype=torch.float32)
    out = fused(dummy)
    print(f"traced with {samples} samples -> output {tuple(out.shape)}")

    torch.onnx.export(
        fused,
        dummy,
        args.out,
        do_constant_folding=True,
        opset_version=17,  # 17 has a real STFT op; 12 cannot express torch.stft
        input_names=["waveform"],
        output_names=["prob"],
        dynamic_axes={"waveform": {0: "batch", 1: "samples"}, "prob": {0: "batch"}},
    )
    print("wrote", args.out)


if __name__ == "__main__":
    main()
