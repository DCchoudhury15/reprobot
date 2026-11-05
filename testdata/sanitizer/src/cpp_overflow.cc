// Heap buffer overflow inside a C++ method, to check name handling.
#include <cstddef>
#include <cstdio>

namespace store {

class Buffer {
public:
	explicit Buffer(std::size_t n) : data_(new int[n]), size_(n) {}
	~Buffer() { delete[] data_; }
	int at(std::size_t i) const;
	std::size_t size() const { return size_; }

private:
	int *data_;
	std::size_t size_;
};

int Buffer::at(std::size_t i) const
{
	return data_[i];
}

int last_value(const Buffer &b, std::size_t n)
{
	return b.at(n);
}

} // namespace store

int main(int argc, char **)
{
	store::Buffer b(4);
	std::printf("%d\n", store::last_value(b, b.size() - 1 + argc));
	return 0;
}
