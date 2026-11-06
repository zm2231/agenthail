extension UIConfig {
    func solidSurfaces(dark: Bool, reduceTransparency: Bool) -> Bool {
        !glass || reduceTransparency || !dark
    }
}
