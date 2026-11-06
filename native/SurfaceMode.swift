extension UIConfig {
    func solidSurfaces(reduceTransparency: Bool) -> Bool {
        !glass || reduceTransparency
    }
}
