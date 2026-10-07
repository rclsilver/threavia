package main

import "image"

// backdrop is the canvas the mark sits on in a square icon.
//
// It is transparent: the mark carries its own glow, and a tile behind it would
// paint a black square into every tab bar and launcher that has a colour of its
// own. The square exists only because an icon slot is square.
func backdrop(size int) *image.RGBA {
	return image.NewRGBA(image.Rect(0, 0, size, size))
}
