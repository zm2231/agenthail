import PhotosUI
import SwiftUI
import UIKit
import UniformTypeIdentifiers

struct ComposerAttachmentMenu: View {
    @ObservedObject var model: AgenthailIOSModel
    @State private var choosingPhotos = false
    @State private var photoSelection: [PhotosPickerItem] = []
    @State private var takingPhoto = false
    @State private var choosingFiles = false

    var body: some View {
        Menu {
            Button { choosingPhotos = true } label: { Label("Photo Library", systemImage: "photo.on.rectangle") }
                .accessibilityIdentifier("attach-photos")
            if UIImagePickerController.isSourceTypeAvailable(.camera) {
                Button { takingPhoto = true } label: { Label("Take Photo", systemImage: "camera") }
                    .accessibilityIdentifier("attach-camera")
            }
            Button(action: pasteImage) { Label("Paste Image", systemImage: "doc.on.clipboard") }
                .accessibilityIdentifier("attach-paste")
            Button { choosingFiles = true } label: { Label("Choose File", systemImage: "folder") }
                .accessibilityIdentifier("attach-files")
        } label: {
            Image(systemName: "plus").font(.system(size: 17, weight: .semibold)).frame(width: 44, height: 44)
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Attach photos or files")
        .accessibilityIdentifier("composer-attach")
        .photosPicker(isPresented: $choosingPhotos, selection: $photoSelection, maxSelectionCount: ComposerAttachments.maxCount, matching: .images)
        .onChange(of: photoSelection) { _, items in
            guard !items.isEmpty else { return }
            photoSelection = []
            Task { await addPhotos(items) }
        }
        .fullScreenCover(isPresented: $takingPhoto) {
            CameraPicker { image in
                takingPhoto = false
                guard let image, let data = image.jpegData(compressionQuality: 0.9) else { return }
                addImage(data, name: "Photo.jpg")
            }
            .ignoresSafeArea()
        }
        .fileImporter(isPresented: $choosingFiles, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
            guard case let .success(urls) = result else { return }
            addFiles(urls)
        }
    }

    private func addPhotos(_ items: [PhotosPickerItem]) async {
        var images: [ComposerAttachment] = []
        var problem: ComposerAttachmentProblem?
        for (index, item) in items.enumerated() {
            let name = "Photo\(items.count > 1 ? "-\(index + 1)" : "").jpg"
            if let data = try? await item.loadTransferable(type: Data.self), let image = ComposerAttachments.image(data, name: name) {
                images.append(image)
            } else {
                problem = problem ?? .unreadable(name)
            }
        }
        model.addAttachments(images, problem: problem)
    }

    private func pasteImage() {
        let pasteboard = UIPasteboard.general
        if let data = pasteboard.data(forPasteboardType: UTType.png.identifier) ?? pasteboard.data(forPasteboardType: UTType.jpeg.identifier) ?? pasteboard.image?.pngData() {
            addImage(data, name: "Pasted.png")
        } else {
            model.addAttachments([], problem: .unreadable("The pasted image"))
        }
    }

    private func addImage(_ data: Data, name: String) {
        if let image = ComposerAttachments.image(data, name: name) {
            model.addAttachments([image])
        } else {
            model.addAttachments([], problem: .unreadable(name))
        }
    }

    private func addFiles(_ urls: [URL]) {
        var files: [ComposerAttachment] = []
        var problem: ComposerAttachmentProblem?
        for url in urls {
            let scoped = url.startAccessingSecurityScopedResource()
            defer { if scoped { url.stopAccessingSecurityScopedResource() } }
            do throws(ComposerAttachmentProblem) {
                files.append(try ComposerAttachments.file(at: url))
            } catch {
                problem = problem ?? error
            }
        }
        model.addAttachments(files, problem: problem)
    }
}

struct ComposerAttachmentStrip: View {
    @ObservedObject var model: AgenthailIOSModel

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(model.composerAttachments) { attachment in
                    ZStack(alignment: .topTrailing) {
                        thumbnail(attachment)
                            .frame(width: 64, height: 64)
                            .clipShape(RoundedRectangle(cornerRadius: 12))
                            .accessibilityLabel(attachment.isImage ? "Image \(attachment.name)" : "File \(attachment.name)")
                        Button { model.removeAttachment(attachment.id) } label: {
                            Image(systemName: "xmark.circle.fill")
                                .font(.system(size: 20))
                                .symbolRenderingMode(.palette)
                                .foregroundStyle(.white, .black.opacity(0.6))
                                .frame(width: 44, height: 44, alignment: .topTrailing)
                        }
                        .buttonStyle(.plain)
                        .offset(x: 10, y: -10)
                        .accessibilityLabel("Remove \(attachment.name)")
                    }
                    .accessibilityIdentifier("composer-attachment")
                }
            }
            .padding(.horizontal, 8).padding(.top, 14)
        }
    }

    @ViewBuilder
    private func thumbnail(_ attachment: ComposerAttachment) -> some View {
        if attachment.isImage, let image = UIImage(data: attachment.data) {
            Image(uiImage: image).resizable().scaledToFill()
        } else {
            VStack(spacing: 4) {
                Image(systemName: "doc").font(.title3)
                Text(attachment.name).font(.caption2).lineLimit(2).multilineTextAlignment(.center)
            }
            .padding(4)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Color.primary.opacity(0.06))
        }
    }
}

struct CameraPicker: UIViewControllerRepresentable {
    let completion: (UIImage?) -> Void

    func makeUIViewController(context: Context) -> UIImagePickerController {
        let picker = UIImagePickerController()
        picker.sourceType = .camera
        picker.delegate = context.coordinator
        return picker
    }

    func updateUIViewController(_: UIImagePickerController, context _: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(completion: completion) }

    final class Coordinator: NSObject, UIImagePickerControllerDelegate, UINavigationControllerDelegate {
        let completion: (UIImage?) -> Void

        init(completion: @escaping (UIImage?) -> Void) { self.completion = completion }

        func imagePickerController(_: UIImagePickerController, didFinishPickingMediaWithInfo info: [UIImagePickerController.InfoKey: Any]) {
            completion(info[.originalImage] as? UIImage)
        }

        func imagePickerControllerDidCancel(_: UIImagePickerController) { completion(nil) }
    }
}
