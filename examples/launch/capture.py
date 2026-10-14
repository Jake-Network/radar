#!/usr/bin/env python3
"""Capture the checked migration demo into a 30-second GIF without dependencies.

Pixels contain actual current demo output. Fixture static PASS is not test or
historical recall evidence. Outputs GIF plus the unabridged terminal transcript.
"""
import argparse
from pathlib import Path
import struct
import subprocess
import tempfile
import textwrap

# Compact 5x7 bitmap font; display uppercase while transcript preserves exact text.
GLYPHS = {
'A':'01110 10001 10001 11111 10001 10001 10001','B':'11110 10001 10001 11110 10001 10001 11110',
'C':'01111 10000 10000 10000 10000 10000 01111','D':'11110 10001 10001 10001 10001 10001 11110',
'E':'11111 10000 10000 11110 10000 10000 11111','F':'11111 10000 10000 11110 10000 10000 10000',
'G':'01111 10000 10000 10111 10001 10001 01111','H':'10001 10001 10001 11111 10001 10001 10001',
'I':'11111 00100 00100 00100 00100 00100 11111','J':'00111 00010 00010 00010 10010 10010 01100',
'K':'10001 10010 10100 11000 10100 10010 10001','L':'10000 10000 10000 10000 10000 10000 11111',
'M':'10001 11011 10101 10101 10001 10001 10001','N':'10001 11001 10101 10011 10001 10001 10001',
'O':'01110 10001 10001 10001 10001 10001 01110','P':'11110 10001 10001 11110 10000 10000 10000',
'Q':'01110 10001 10001 10001 10101 10010 01101','R':'11110 10001 10001 11110 10100 10010 10001',
'S':'01111 10000 10000 01110 00001 00001 11110','T':'11111 00100 00100 00100 00100 00100 00100',
'U':'10001 10001 10001 10001 10001 10001 01110','V':'10001 10001 10001 10001 10001 01010 00100',
'W':'10001 10001 10001 10101 10101 11011 10001','X':'10001 10001 01010 00100 01010 10001 10001',
'Y':'10001 10001 01010 00100 00100 00100 00100','Z':'11111 00001 00010 00100 01000 10000 11111',
'0':'01110 10001 10011 10101 11001 10001 01110','1':'00100 01100 00100 00100 00100 00100 01110',
'2':'01110 10001 00001 00010 00100 01000 11111','3':'11110 00001 00001 01110 00001 00001 11110',
'4':'00010 00110 01010 10010 11111 00010 00010','5':'11111 10000 10000 11110 00001 00001 11110',
'6':'01110 10000 10000 11110 10001 10001 01110','7':'11111 00001 00010 00100 01000 01000 01000',
'8':'01110 10001 10001 01110 10001 10001 01110','9':'01110 10001 10001 01111 00001 00001 01110',
' ':'00000 00000 00000 00000 00000 00000 00000','-':'00000 00000 00000 11111 00000 00000 00000',
':':'00000 00100 00100 00000 00100 00100 00000','.':'00000 00000 00000 00000 00000 00100 00100',
'/':'00001 00010 00010 00100 01000 01000 10000','_':'00000 00000 00000 00000 00000 00000 11111',
'?':'01110 10001 00001 00010 00100 00000 00100','!':'00100 00100 00100 00100 00100 00000 00100',
'=':'00000 00000 11111 00000 11111 00000 00000','+':'00000 00100 00100 11111 00100 00100 00000',
'(':'00010 00100 01000 01000 01000 00100 00010',')':'01000 00100 00010 00010 00010 00100 01000',
'"':'01010 01010 00000 00000 00000 00000 00000',"'":'00100 00100 00000 00000 00000 00000 00000',
}
WIDTH, HEIGHT = 960, 440


def frame(title, output):
    pixels = bytearray(WIDTH * HEIGHT)
    relevant = [line for line in output.splitlines() if any(token in line for token in
        ('Radar gate:', '✓ Merge', 'Migrations', 'migration_multiple_heads', 'look at:', 'fix:'))]
    wrapped = [part for line in relevant for part in textwrap.wrap(line.strip(), width=74)]
    lines = [title, '', 'STATIC FIXTURE DEMO - NO REPOSITORY TESTS RUN', ''] + wrapped[:13]
    for row, line in enumerate(lines):
        normalized = line.upper().replace('✓', '+').replace('✗', '!').replace('·', '.').replace('—', '-').replace('→', '-')
        for col, char in enumerate(normalized[:76]):
            for y, bits in enumerate(GLYPHS.get(char, GLYPHS[' ']).split()):
                for x, bit in enumerate(bits):
                    if bit == '1':
                        for dy in range(2):
                            for dx in range(2):
                                pixels[(18 + row * 23 + y * 2 + dy) * WIDTH + 18 + col * 12 + x * 2 + dx] = 1
    return pixels


def literal_lzw(pixels):
    # Clearing before every 200 literals keeps the code width at nine bits.
    codes = []
    for start in range(0, len(pixels), 200):
        codes.append(256)
        codes.extend(pixels[start:start + 200])
    codes.append(257)
    data, pending, count = bytearray(), 0, 0
    for code in codes:
        pending |= code << count
        count += 9
        while count >= 8:
            data.append(pending & 255)
            pending >>= 8
            count -= 8
    if count:
        data.append(pending & 255)
    blocks = bytearray([8])
    for start in range(0, len(data), 255):
        block = data[start:start + 255]
        blocks.append(len(block))
        blocks.extend(block)
    blocks.append(0)
    return blocks


def gif(frames):
    data = bytearray(b'GIF89a') + struct.pack('<HHBBB', WIDTH, HEIGHT, 0xF7, 0, 0)
    data.extend(bytes([17, 24, 39, 226, 232, 240]) + bytes(254 * 3))
    data.extend(b'!\xff\x0bNETSCAPE2.0\x03\x01\x00\x00\x00')
    for (title, output), delay in zip(frames, [500, 500, 1000, 1000]):
        data.extend(b'!\xf9\x04\x04' + struct.pack('<H', delay) + b'\x00\x00')
        data.extend(b',' + struct.pack('<HHHHB', 0, 0, WIDTH, HEIGHT, 0))
        data.extend(literal_lzw(frame(title, output)))
    data.append(0x3B)
    return data


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--radar', required=True)
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[2]
    script = (root / 'examples/migrations/demo.sh').read_text()
    # Instrument the trusted demo, retaining its real assertions and cleanup.
    script = script.replace('  expect "$workspace/alone.txt" "✓ Migrations"',
        '  expect "$workspace/alone.txt" "✓ Migrations"\n  echo "SCENE:$branch"\n  cat "$workspace/alone.txt"')
    script = script.replace('# Repair: the refunds revision now follows the orders revision.',
        'echo "SCENE:combined branches"\ncat "$workspace/combined.txt"\n# Repair: the refunds revision now follows the orders revision.')
    script = script.replace('echo "migrations demo: ok"',
        'echo "SCENE:corrected migration chain"\ncat "$workspace/repaired.txt"')
    with tempfile.TemporaryDirectory(prefix='radar-launch-') as directory:
        instrumented = Path(directory) / 'demo.sh'
        instrumented.write_text(script)
        proc = subprocess.run(['bash', str(instrumented), str(Path(args.radar).resolve())], cwd=root,
                              text=True, capture_output=True, timeout=180, check=True)
    scenes = [(part.split('\n', 1)[0], part.split('\n', 1)[1]) for part in proc.stdout.split('SCENE:')[1:]]
    if len(scenes) != 4 or not all('Radar gate:' in text for _, text in scenes):
        raise ValueError('Demo did not produce four verified scenes.')
    output = Path(args.output)
    output.write_bytes(gif(scenes))
    output.with_suffix('.txt').write_text(proc.stdout)
    print(str(output))


if __name__ == '__main__':
    main()
