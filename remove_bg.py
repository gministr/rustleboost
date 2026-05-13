from PIL import Image

img = Image.open(r'C:\Users\user\vpn-client\logo.png').convert('RGBA')
pixels = img.load()
w, h = img.size

removed = 0
for y in range(h):
    for x in range(w):
        r, g, b, a = pixels[x, y]
        brightness = (r + g + b) / 3
        is_neutral = abs(r - g) < 25 and abs(g - b) < 25 and abs(r - b) < 25
        # Remove light neutral-grey checkerboard background
        if is_neutral and brightness > 160:
            pixels[x, y] = (0, 0, 0, 0)
            removed += 1

img.save(r'C:\Users\user\vpn-client\logo_clean.png', 'PNG')
total = w * h
print(f'Removed {removed} px ({removed * 100 // total}%) of {total} total')
print('Saved: logo_clean.png')
