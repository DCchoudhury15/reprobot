/* Asks malloc for more memory than ASan allows. The header line of this
 * report does not name the bug type; only the SUMMARY line does. */
#include <stdio.h>
#include <stdlib.h>

void *make_table(size_t rows, size_t row_size)
{
	return malloc(rows * row_size);
}

int main(int argc, char **argv)
{
	(void)argv;
	char *t = make_table((size_t)1 << 40, 16 * (size_t)argc);
	printf("%p\n", (void *)t);
	free(t);
	return 0;
}
